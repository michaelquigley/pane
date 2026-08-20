package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/michaelquigley/df/dd"
)

type Config struct {
	Endpoint             string
	ApiKey               string
	Model                string
	System               string
	Listen               string
	ContextWindows       map[string]int
	DefaultContextWindow int
	MaxTokens            map[string]int
	DefaultMaxTokens     int
	IncludeUsage         bool
	DataDir              string
	MCP                  *MCPConfig
}

type MCPConfig struct {
	Servers   map[string]*ServerConfig
	Separator string
}

type ServerConfig struct {
	Command string `dd:",+required"`
	Args    []string
	Env     map[string]string
	Approve bool
	Timeout string
}

func DefaultConfig() *Config {
	return &Config{
		Endpoint:     "http://localhost:18080/v1",
		Model:        "qwen2.5:14b",
		System:       "You are a helpful assistant.",
		Listen:       "127.0.0.1:8400",
		IncludeUsage: true,
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
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}
	if c.Listen == "" {
		return fmt.Errorf("listen address is required")
	}
	for model, window := range c.ContextWindows {
		if window <= 0 {
			return fmt.Errorf("context window for %q must be greater than zero", model)
		}
	}
	if c.DefaultContextWindow < 0 {
		return fmt.Errorf("default context window must be greater than zero when set")
	}
	if c.MCP != nil {
		for name, sc := range c.MCP.Servers {
			if sc.Command == "" {
				return fmt.Errorf("mcp server %q: command is required", name)
			}
			if sc.Timeout != "" {
				if _, err := time.ParseDuration(sc.Timeout); err != nil {
					return fmt.Errorf("mcp server %q: invalid timeout %q: %w", name, sc.Timeout, err)
				}
			}
		}
	}
	return nil
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

// SessionDataDir resolves the directory the session store lives under: the
// configured DataDir when set, else $XDG_DATA_HOME/pane, else
// ~/.local/share/pane. a leading '~' in the configured value expands to the
// user's home directory, so the documented example works as written.
func (c *Config) SessionDataDir() (string, error) {
	if c.DataDir != "" {
		return expandHome(c.DataDir)
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "pane"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "pane"), nil
}

// expandHome expands a leading '~' path element to the user's home directory.
// a '~' that is not its own path element (as in '~other/dir') is left alone.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expanding '%s': %w", path, err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
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
