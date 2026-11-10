// nexus 统一 LLM 与算力服务平台入口。
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nexus/internal/config"
	"nexus/internal/gateway"
	"nexus/internal/logging"
	"nexus/internal/metrics"
	"nexus/internal/server"
	"nexus/internal/store"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fallback := slog.New(slog.NewTextHandler(os.Stderr, nil))
		fallback.Error("failed to load configuration", "path", *configPath, "error", err)
		os.Exit(1)
	}
	logger := logging.New(cfg.Log.Level, cfg.Log.Format)

	reg := metrics.NewRegistry()
	st := store.BuildFromConfig(cfg)
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
