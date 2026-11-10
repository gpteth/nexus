// Package config 定义 Nexus 的配置模型、加载、默认值与校验。
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration 包装 time.Duration，使其可从 "30s" 这样的 YAML 字符串解析。
type Duration struct {
	time.Duration
}

// UnmarshalYAML 实现时长字符串解析。
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = parsed
	return nil
}

// Config 是平台根配置。
type Config struct {
	Server  ServerConfig   `yaml:"server"`
	Log     LogConfig      `yaml:"log"`
	Gateway GatewayConfig  `yaml:"gateway"`
	Auth    AuthConfig     `yaml:"auth"`
	Tenants []TenantConfig `yaml:"tenants"`
	Metrics MetricsConfig  `yaml:"metrics"`
}

// ServerConfig 是 HTTP 服务配置。
type ServerConfig struct {
	Listen           string   `yaml:"listen"`
	ReadTimeout      Duration `yaml:"read_timeout"`
	WriteTimeout     Duration `yaml:"write_timeout"`
	IdleTimeout      Duration `yaml:"idle_timeout"`
	ShutdownTimeout  Duration `yaml:"shutdown_timeout"`
	CORSAllowOrigins []string `yaml:"cors_allow_origins"`
}

// LogConfig 是日志配置。
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// GatewayConfig 是网关与上游配置。
type GatewayConfig struct {
	DefaultTimeout Duration         `yaml:"default_timeout"`
	Retry          RetryConfig      `yaml:"retry"`
	Breaker        BreakerConfig    `yaml:"breaker"`
	RateLimit      RateLimitConfig  `yaml:"ratelimit"`
	Upstreams      []UpstreamConfig `yaml:"upstreams"`
	Routes         []RouteConfig    `yaml:"routes"`
}

// RetryConfig 是重试策略。
type RetryConfig struct {
	MaxAttempts int      `yaml:"max_attempts"`
	BaseDelay   Duration `yaml:"base_delay"`
	MaxDelay    Duration `yaml:"max_delay"`
}

// BreakerConfig 是熔断器配置。
type BreakerConfig struct {
	FailureThreshold int      `yaml:"failure_threshold"`
	OpenDuration     Duration `yaml:"open_duration"`
	HalfOpenProbes   int      `yaml:"half_open_probes"`
}

// RateLimitConfig 是默认限流配置。
type RateLimitConfig struct {
	Enabled           bool `yaml:"enabled"`
	RequestsPerMinute int  `yaml:"requests_per_minute"`
}

// UpstreamConfig 描述一个模型上游。
type UpstreamConfig struct {
	Name            string   `yaml:"name"`
	Type            string   `yaml:"type"`
	BaseURL         string   `yaml:"base_url"`
	APIKey          string   `yaml:"api_key"`
	APIKeyEncrypted string   `yaml:"api_key_encrypted"` // AES-256-GCM 密文，启动时用主密钥解密
	Weight          int      `yaml:"weight"`
	Models          []string `yaml:"models"`
	HealthCheckPath string   `yaml:"health_check_path"`
	MaxConnections  int      `yaml:"max_connections"`
}

// RouteConfig 把一个逻辑模型映射到一组上游。
type RouteConfig struct {
	Model     string   `yaml:"model"`
	Upstreams []string `yaml:"upstreams"`
	Strategy  string   `yaml:"strategy"`
}

// AuthConfig 是数据面鉴权配置。
type AuthConfig struct {
	Keys []KeyConfig `yaml:"keys"`
}

// KeyConfig 是一个平台 API Key。
type KeyConfig struct {
	Key               string   `yaml:"key"`
	Tenant            string   `yaml:"tenant"`
	Enabled           *bool    `yaml:"enabled"`
	RequestsPerMinute int      `yaml:"requests_per_minute"`
	Models            []string `yaml:"models"`
}

// TenantConfig 是租户配置。
type TenantConfig struct {
	ID                string  `yaml:"id"`
	Name              string  `yaml:"name"`
	RequestsPerMinute int     `yaml:"requests_per_minute"`
	BudgetUSD         float64 `yaml:"budget_usd"`
}

// MetricsConfig 是指标暴露配置。
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

// Load 读取配置文件，展开环境变量，应用默认值并校验。
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	// 先展开 ${VAR} 形式的环境变量，再解析 YAML。
	expanded := os.ExpandEnv(string(raw))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) setDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8080"
	}
	if c.Server.ReadTimeout.Duration == 0 {
		c.Server.ReadTimeout.Duration = 15 * time.Second
	}
	if c.Server.WriteTimeout.Duration == 0 {
		// 流式响应需要较长写超时。
		c.Server.WriteTimeout.Duration = 10 * time.Minute
	}
	if c.Server.IdleTimeout.Duration == 0 {
		c.Server.IdleTimeout.Duration = 60 * time.Second
	}
	if c.Server.ShutdownTimeout.Duration == 0 {
		c.Server.ShutdownTimeout.Duration = 20 * time.Second
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "json"
	}
	if c.Gateway.DefaultTimeout.Duration == 0 {
		c.Gateway.DefaultTimeout.Duration = 120 * time.Second
	}
	if c.Gateway.Retry.MaxAttempts == 0 {
		c.Gateway.Retry.MaxAttempts = 3
	}
	if c.Gateway.Retry.BaseDelay.Duration == 0 {
		c.Gateway.Retry.BaseDelay.Duration = 500 * time.Millisecond
	}
	if c.Gateway.Retry.MaxDelay.Duration == 0 {
		c.Gateway.Retry.MaxDelay.Duration = 5 * time.Second
	}
	if c.Gateway.Breaker.FailureThreshold == 0 {
		c.Gateway.Breaker.FailureThreshold = 5
	}
	if c.Gateway.Breaker.OpenDuration.Duration == 0 {
		c.Gateway.Breaker.OpenDuration.Duration = 30 * time.Second
	}
	if c.Gateway.Breaker.HalfOpenProbes == 0 {
		c.Gateway.Breaker.HalfOpenProbes = 1
	}
	if c.Metrics.Path == "" {
		c.Metrics.Path = "/metrics"
	}
	for i := range c.Gateway.Upstreams {
		if c.Gateway.Upstreams[i].Weight == 0 {
			c.Gateway.Upstreams[i].Weight = 1
		}
	}
}

// Validate 检查配置的内部一致性。
func (c *Config) Validate() error {
	upstreamByName := map[string]bool{}
	for i, u := range c.Gateway.Upstreams {
		if u.Name == "" {
			return fmt.Errorf("gateway.upstreams[%d]: name is required", i)
		}
		if upstreamByName[u.Name] {
			return fmt.Errorf("gateway.upstreams: duplicate name %q", u.Name)
		}
		upstreamByName[u.Name] = true
		if u.BaseURL == "" {
			return fmt.Errorf("gateway.upstreams[%s]: base_url is required", u.Name)
		}
		if !strings.HasPrefix(u.BaseURL, "http://") && !strings.HasPrefix(u.BaseURL, "https://") {
			return fmt.Errorf("gateway.upstreams[%s]: base_url must start with http(s)://", u.Name)
		}
	}
	for i, r := range c.Gateway.Routes {
		if r.Model == "" {
			return fmt.Errorf("gateway.routes[%d]: model is required", i)
		}
		if len(r.Upstreams) == 0 {
			return fmt.Errorf("gateway.routes[%s]: at least one upstream is required", r.Model)
		}
		for _, u := range r.Upstreams {
			if !upstreamByName[u] {
				return fmt.Errorf("gateway.routes[%s]: unknown upstream %q", r.Model, u)
			}
		}
	}
	tenants := map[string]bool{}
	for _, t := range c.Tenants {
		if t.ID == "" {
			return fmt.Errorf("tenants: id is required")
		}
		if tenants[t.ID] {
			return fmt.Errorf("tenants: duplicate id %q", t.ID)
		}
		tenants[t.ID] = true
	}
	for i, k := range c.Auth.Keys {
		if k.Key == "" {
			return fmt.Errorf("auth.keys[%d]: key is required", i)
		}
		if k.Tenant != "" && len(tenants) > 0 && !tenants[k.Tenant] {
			return fmt.Errorf("auth.keys[%d]: unknown tenant %q", i, k.Tenant)
		}
	}
	return nil
}
