package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	Models               map[string]*ModelConfig
	IncludeUsage         bool
	DataDir              string
	MCP                  *MCPConfig
}

type ModelConfig struct {
	Endpoint      string
	UpstreamModel string
	ApiKey        *string
	ContextWindow *int
	MaxTokens     *int
}

type ResolvedModel struct {
	Alias         string
	Endpoint      string
	UpstreamModel string
	ApiKey        string
	ContextWindow int
	MaxTokens     int
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

func NewConfig() *Config {
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
	cfg := NewConfig()
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
	if !c.HasModelRegistry() && strings.TrimSpace(c.Endpoint) == "" {
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
	for model, maxTokens := range c.MaxTokens {
		if maxTokens <= 0 {
			return fmt.Errorf("max tokens for %q must be greater than zero", model)
		}
	}
	if c.DefaultMaxTokens < 0 {
		return fmt.Errorf("default max tokens must be greater than zero when set")
	}
	if c.HasModelRegistry() {
		if _, ok := c.Models[c.Model]; !ok || strings.TrimSpace(c.Model) == "" {
			return fmt.Errorf("default model '%s' is not in the model registry", c.Model)
		}
		for alias, model := range c.Models {
			if strings.TrimSpace(alias) == "" {
				return fmt.Errorf("model registry key must not be blank")
			}
			if model == nil {
				return fmt.Errorf("model '%s': configuration is required", alias)
			}
			resolved, ok := c.ResolveModel(alias)
			if !ok {
				return fmt.Errorf("model '%s': could not be resolved", alias)
			}
			if strings.TrimSpace(resolved.Endpoint) == "" {
				return fmt.Errorf("model '%s': endpoint is required", alias)
			}
			if strings.TrimSpace(resolved.UpstreamModel) == "" {
				return fmt.Errorf("model '%s': upstream model is required", alias)
			}
			if model.ContextWindow != nil && *model.ContextWindow <= 0 {
				return fmt.Errorf("model '%s': context window must be greater than zero", alias)
			}
			if model.MaxTokens != nil && *model.MaxTokens <= 0 {
				return fmt.Errorf("model '%s': max tokens must be greater than zero", alias)
			}
		}
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

func (c *Config) HasModelRegistry() bool {
	return len(c.Models) > 0
}

func (c *Config) ResolveModel(requested string) (ResolvedModel, bool) {
	alias := requested
	if strings.TrimSpace(alias) == "" {
		alias = c.Model
	}

	if !c.HasModelRegistry() {
		if strings.TrimSpace(alias) == "" {
			return ResolvedModel{}, false
		}
		return ResolvedModel{
			Alias:         alias,
			Endpoint:      c.Endpoint,
			UpstreamModel: alias,
			ApiKey:        c.ApiKey,
			ContextWindow: resolveLegacyContextWindow(alias, c),
			MaxTokens:     resolveLegacyMaxTokens(alias, c),
		}, true
	}

	model, ok := c.Models[alias]
	if !ok || model == nil {
		return ResolvedModel{}, false
	}

	endpoint := model.Endpoint
	if strings.TrimSpace(endpoint) == "" {
		endpoint = c.Endpoint
	}
	upstreamModel := model.UpstreamModel
	if strings.TrimSpace(upstreamModel) == "" {
		upstreamModel = alias
	}
	apiKey := c.ApiKey
	if model.ApiKey != nil {
		apiKey = *model.ApiKey
	}
	contextWindow := 0
	if model.ContextWindow != nil {
		contextWindow = *model.ContextWindow
	}
	maxTokens := 0
	if model.MaxTokens != nil {
		maxTokens = *model.MaxTokens
	}

	return ResolvedModel{
		Alias:         alias,
		Endpoint:      endpoint,
		UpstreamModel: upstreamModel,
		ApiKey:        apiKey,
		ContextWindow: contextWindow,
		MaxTokens:     maxTokens,
	}, true
}

func (c *Config) ResolvedModels() []ResolvedModel {
	if !c.HasModelRegistry() {
		return nil
	}

	aliases := make([]string, 0, len(c.Models))
	for alias := range c.Models {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	models := make([]ResolvedModel, 0, len(aliases))
	for _, alias := range aliases {
		if model, ok := c.ResolveModel(alias); ok {
			models = append(models, model)
		}
	}
	return models
}

func resolveLegacyContextWindow(model string, cfg *Config) int {
	if window, ok := cfg.ContextWindows[model]; ok {
		return window
	}
	return cfg.DefaultContextWindow
}

func resolveLegacyMaxTokens(model string, cfg *Config) int {
	if cap, ok := cfg.MaxTokens[model]; ok {
		return cap
	}
	return cfg.DefaultMaxTokens
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
