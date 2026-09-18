package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig_Valid(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	yamlContent := `
listen_address: ":9090"
backends:
  - "http://127.0.0.1:8081"
  - "http://127.0.0.1:8082"
strategy: "least_conn"
health_check:
  interval: "3s"
  timeout: "1s"
  path: "/ping"
timeouts:
  read_timeout: "10s"
  read_header_timeout: "3s"
  write_timeout: "10s"
  idle_timeout: "30s"
retry:
  max_retries: 3
  passive_failure_threshold: 4
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load valid config: %v", err)
	}

	if cfg.Port != ":9090" {
		t.Errorf("expected port :9090, got %s", cfg.Port)
	}
	if len(cfg.Backends) != 2 {
		t.Errorf("expected 2 backends, got %d", len(cfg.Backends))
	}
	if cfg.Strategy != "least_conn" {
		t.Errorf("expected least_conn, got %s", cfg.Strategy)
	}
	if cfg.HealthCheck.Interval != 3*time.Second {
		t.Errorf("expected 3s interval, got %v", cfg.HealthCheck.Interval)
	}
	if cfg.HealthCheck.Path != "/ping" {
		t.Errorf("expected /ping path, got %s", cfg.HealthCheck.Path)
	}
	if cfg.Retry.MaxRetries != 3 {
		t.Errorf("expected 3 max_retries, got %d", cfg.Retry.MaxRetries)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "minimal.yaml")

	yamlContent := `
backends:
  - "http://127.0.0.1:8081"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load minimal config: %v", err)
	}

	if cfg.Port != ":8080" {
		t.Errorf("expected default port :8080, got %s", cfg.Port)
	}
	if cfg.Strategy != "round_robin" {
		t.Errorf("expected default strategy round_robin, got %s", cfg.Strategy)
	}
	if cfg.HealthCheck.Path != "/health" {
		t.Errorf("expected default /health path, got %s", cfg.HealthCheck.Path)
	}
	if cfg.HealthCheck.Interval != 5*time.Second {
		t.Errorf("expected default 5s interval, got %v", cfg.HealthCheck.Interval)
	}
}

func TestLoadConfig_NotFound(t *testing.T) {
	_, err := LoadConfig("non_existent_file.yaml")
	if err == nil {
		t.Fatal("expected error for non existent file, got nil")
	}
}
