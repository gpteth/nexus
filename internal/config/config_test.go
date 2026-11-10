package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndEnvExpansion(t *testing.T) {
	os.Setenv("NEXUS_TEST_KEY", "secret-value")
	defer os.Unsetenv("NEXUS_TEST_KEY")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server:
  listen: ":9090"
gateway:
  upstreams:
    - name: "up"
      base_url: "https://example.com"
      api_key: "${NEXUS_TEST_KEY}"
auth:
  keys:
    - key: "sk-1"
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != ":9090" {
		t.Fatalf("listen not parsed: %s", cfg.Server.Listen)
	}
	if cfg.Gateway.Upstreams[0].APIKey != "secret-value" {
		t.Fatalf("env var not expanded: %s", cfg.Gateway.Upstreams[0].APIKey)
	}
	if cfg.Server.ShutdownTimeout.Duration == 0 {
		t.Fatal("default shutdown timeout was not applied")
	}
	if cfg.Gateway.Upstreams[0].Weight != 1 {
		t.Fatal("default weight was not applied")
	}
}

func TestValidateRejectsBadURL(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{Upstreams: []UpstreamConfig{{Name: "up", BaseURL: "not-a-url"}}}}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for malformed base_url")
	}
}

func TestValidateRejectsUnknownRouteUpstream(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{
		Upstreams: []UpstreamConfig{{Name: "up", BaseURL: "https://example.com"}},
		Routes:    []RouteConfig{{Model: "m", Upstreams: []string{"missing"}}},
	}}
	cfg.setDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for route referencing unknown upstream")
	}
}
