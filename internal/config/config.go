package config

import (
	"fmt"
	"os"
	"time"

	"github.com/goccy/go-yaml"
)

type HealthCheckConfig struct {
	Interval time.Duration `yaml:"-"`
	Timeout  time.Duration `yaml:"-"`
	Path     string        `yaml:"path"`

	RawInterval string `yaml:"interval"`
	RawTimeout  string `yaml:"timeout"`
}

type TimeoutsConfig struct {
	ReadTimeout       time.Duration `yaml:"-"`
	ReadHeaderTimeout time.Duration `yaml:"-"`
	WriteTimeout      time.Duration `yaml:"-"`
	IdleTimeout       time.Duration `yaml:"-"`

	RawReadTimeout       string `yaml:"read_timeout"`
	RawReadHeaderTimeout string `yaml:"read_header_timeout"`
	RawWriteTimeout      string `yaml:"write_timeout"`
	RawIdleTimeout       string `yaml:"idle_timeout"`
}

type RetryConfig struct {
	MaxRetries              int `yaml:"max_retries"`
	PassiveFailureThreshold int `yaml:"passive_failure_threshold"`
}

type Config struct {
	Port        string            `yaml:"listen_address"`
	Backends    []string          `yaml:"backends"`
	Strategy    string            `yaml:"strategy"`
	HealthCheck HealthCheckConfig `yaml:"health_check"`
	Timeouts    TimeoutsConfig    `yaml:"timeouts"`
	Retry       RetryConfig       `yaml:"retry"`
}

// LoadConfig reads YAML configuration and parses duration fields with defaults.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	setDefaults(&cfg)

	if err := parseDurations(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Port == "" {
		cfg.Port = ":8080"
	}
	if cfg.Strategy == "" {
		cfg.Strategy = "round_robin"
	}
	if cfg.HealthCheck.Path == "" {
		cfg.HealthCheck.Path = "/health"
	}
	if cfg.Retry.MaxRetries <= 0 {
		cfg.Retry.MaxRetries = 2
	}
	if cfg.Retry.PassiveFailureThreshold <= 0 {
		cfg.Retry.PassiveFailureThreshold = 3
	}
}

func parseDurations(cfg *Config) error {
	var err error

	// Health check durations
	if cfg.HealthCheck.RawInterval != "" {
		cfg.HealthCheck.Interval, err = time.ParseDuration(cfg.HealthCheck.RawInterval)
		if err != nil {
			return fmt.Errorf("invalid health_check.interval: %w", err)
		}
	} else {
		cfg.HealthCheck.Interval = 5 * time.Second
	}

	if cfg.HealthCheck.RawTimeout != "" {
		cfg.HealthCheck.Timeout, err = time.ParseDuration(cfg.HealthCheck.RawTimeout)
		if err != nil {
			return fmt.Errorf("invalid health_check.timeout: %w", err)
		}
	} else {
		cfg.HealthCheck.Timeout = 2 * time.Second
	}

	// Server timeout durations
	if cfg.Timeouts.RawReadTimeout != "" {
		cfg.Timeouts.ReadTimeout, err = time.ParseDuration(cfg.Timeouts.RawReadTimeout)
		if err != nil {
			return fmt.Errorf("invalid timeouts.read_timeout: %w", err)
		}
	} else {
		cfg.Timeouts.ReadTimeout = 15 * time.Second
	}

	if cfg.Timeouts.RawReadHeaderTimeout != "" {
		cfg.Timeouts.ReadHeaderTimeout, err = time.ParseDuration(cfg.Timeouts.RawReadHeaderTimeout)
		if err != nil {
			return fmt.Errorf("invalid timeouts.read_header_timeout: %w", err)
		}
	} else {
		cfg.Timeouts.ReadHeaderTimeout = 5 * time.Second
	}

	if cfg.Timeouts.RawWriteTimeout != "" {
		cfg.Timeouts.WriteTimeout, err = time.ParseDuration(cfg.Timeouts.RawWriteTimeout)
		if err != nil {
			return fmt.Errorf("invalid timeouts.write_timeout: %w", err)
		}
	} else {
		cfg.Timeouts.WriteTimeout = 15 * time.Second
	}

	if cfg.Timeouts.RawIdleTimeout != "" {
		cfg.Timeouts.IdleTimeout, err = time.ParseDuration(cfg.Timeouts.RawIdleTimeout)
		if err != nil {
			return fmt.Errorf("invalid timeouts.idle_timeout: %w", err)
		}
	} else {
		cfg.Timeouts.IdleTimeout = 60 * time.Second
	}

	return nil
}
