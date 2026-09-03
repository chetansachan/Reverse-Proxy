package config

import (
	"log"
	"os"

	"github.com/goccy/go-yaml"
)

type Config struct {
	Port     string   `yaml:"listen_address"`
	Backends []string `yaml:"backends"`
	Strategy string   `yaml:"strategy"`
}

func LoadConfig(path string) (*Config, error) {
	config, err := os.ReadFile(path)
	if err != nil {
		log.Printf("Failed to read file: %v", err)
		return &Config{}, err
	}

	var myConfig Config
	if err := yaml.Unmarshal(config, &myConfig); err != nil {
		log.Printf("Failed to translate YAML: %v", err)
		return &Config{}, err
	}

	return &myConfig, nil
}
