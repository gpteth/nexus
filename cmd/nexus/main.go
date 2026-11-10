// nexus 统一 LLM 与算力服务平台入口。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nexus/internal/config"
	"nexus/internal/gateway"
	"nexus/internal/logging"
	"nexus/internal/metrics"
	"nexus/internal/security"
	"nexus/internal/server"
	"nexus/internal/store"

	_ "modernc.org/sqlite"
)

// masterKeyEnv 是注入主密钥的环境变量，用于解密配置中的上游凭据。
const masterKeyEnv = "NEXUS_MASTER_KEY"

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to configuration file")
	encryptVal := flag.String("encrypt", "", "encrypt a credential with NEXUS_MASTER_KEY and print ciphertext, then exit")
	flag.Parse()

	if *encryptVal != "" {
		if err := runEncrypt(*encryptVal); err != nil {
			slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("encrypt failed", "error", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fallback := slog.New(slog.NewTextHandler(os.Stderr, nil))
		fallback.Error("failed to load configuration", "path", *configPath, "error", err)
		os.Exit(1)
	}
	logger := logging.New(cfg.Log.Level, cfg.Log.Format)

	if err := decryptUpstreams(logger, cfg); err != nil {
		logger.Error("failed to decrypt upstream credentials", "error", err)
		os.Exit(1)
	}

	reg := metrics.NewRegistry()
	st, err := buildStore(cfg)
	if err != nil {
		logger.Error("failed to initialize storage", "error", err)
		os.Exit(1)
	}
	gw := gateway.New(cfg, logger, st, reg)
	gh := gateway.NewHandler(gw, logger)
	srv := server.New(cfg, gh, reg, logger)

	// 后台任务随信号上下文一起停止。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go gw.StartHealthChecks(ctx, 15*time.Second)
	go collectGauges(ctx, gh, 5*time.Second)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run() }()

	select {
	case err := <-errCh:
		if err != nil {
			logger.Error("server terminated with error", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining in-flight requests")
	}

	if err := srv.Shutdown(); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}

func collectGauges(ctx context.Context, gh *gateway.Handler, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	gh.CollectGauges()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			gh.CollectGauges()
		}
	}
}

// decryptUpstreams 用环境变量中的主密钥解密配置内的上游凭据，实现密钥不明文落盘。
func decryptUpstreams(logger *slog.Logger, cfg *config.Config) error {
	need := false
	for _, u := range cfg.Gateway.Upstreams {
		if u.APIKeyEncrypted != "" {
			need = true
		}
	}
	if !need {
		return nil
	}
	master, err := security.LoadMasterKey(os.Getenv(masterKeyEnv))
	if err != nil {
		return err
	}
	cipher, err := security.NewCipher(master)
	if err != nil {
		return err
	}
	for i := range cfg.Gateway.Upstreams {
		u := &cfg.Gateway.Upstreams[i]
		if u.APIKeyEncrypted == "" {
			continue
		}
		plain, err := cipher.Decrypt(u.APIKeyEncrypted)
		if err != nil {
			return err
		}
		u.APIKey = plain
		logger.Info("decrypted upstream credential", "upstream", u.Name)
	}
	return nil
}

// runEncrypt 用主密钥加密凭据并打印密文，便于写入配置。
func runEncrypt(plain string) error {
	master, err := security.LoadMasterKey(os.Getenv(masterKeyEnv))
	if err != nil {
		return err
	}
	cipher, err := security.NewCipher(master)
	if err != nil {
		return err
	}
	enc, err := cipher.Encrypt(plain)
	if err != nil {
		return err
	}
	fmt.Println(enc)
	return nil
}

// buildStore 按配置选择存储：配置 driver 时用 SQL 持久化并 Seed，否则用内存。
func buildStore(cfg *config.Config) (store.Store, error) {
	if cfg.Database.Driver == "" {
		return store.BuildFromConfig(cfg), nil
	}
	sqlStore, err := store.NewSQLStore(cfg.Database.Driver, cfg.Database.DataSource)
	if err != nil {
		return nil, err
	}
	if err := sqlStore.Seed(cfg); err != nil {
		sqlStore.Close()
		return nil, err
	}
	return sqlStore, nil
}
