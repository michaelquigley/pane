package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/michaelquigley/df/dd"
)

const Version = "v0.0.x-dev"

type Config struct {
	Endpoint string
	Model    string
	System   string
	Listen   string
	MCP      *MCPConfig
}

type MCPConfig struct {
	Servers   map[string]*ServerConfig
	Separator string
}

type ServerConfig struct {
	Command string            `dd:",+required"`
	Args    []string
	Env     map[string]string
	Approve bool
	Timeout string
}

func DefaultConfig() *Config {
	return &Config{
		Endpoint: "http://localhost:18080/v1",
		Model:    "qwen2.5:14b",
		System:   "You are a helpful assistant.",
		Listen:   "127.0.0.1:8400",
		MCP: &MCPConfig{
			Separator: "_",
		},
	}
}

func Load(configPath string) (*Config, error) {
	cfg := DefaultConfig()
	if err := mergeIfExists(cfg, globalConfigPath()); err != nil {
		return nil, err
	}
	if err := mergeIfExists(cfg, "./pane.yaml"); err != nil {
		return nil, err
	}
	if configPath != "" {
		if err := dd.MergeYAMLFile(cfg, configPath); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func mergeIfExists(cfg *Config, path string) error {
	err := dd.MergeYAMLFile(cfg, path)
	if err != nil {
		var fileErr *dd.FileError
		if errors.As(err, &fileErr) && fileErr.IsNotFound() {
			return nil
		}
		return err
	}
	return nil
}

func globalConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "pane", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "pane", "config.yaml")
	}
	return filepath.Join(home, ".config", "pane", "config.yaml")
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	return os.ExpandEnv(path)
}
