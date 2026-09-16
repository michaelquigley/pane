package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewConfigIncludesUsage(t *testing.T) {
	t.Parallel()

	cfg := NewConfig()

	if !cfg.IncludeUsage {
		t.Fatalf("expected include_usage default to true")
	}
	if cfg.ContextWindows != nil {
		t.Fatalf("expected context windows to be unset by default")
	}
	if cfg.DefaultContextWindow != 0 {
		t.Fatalf("expected default context window to be unset by default")
	}
}

func TestLoadAllowsIncludeUsageFalseOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	t.Chdir(tmp)

	path := filepath.Join(tmp, "pane.yaml")
	if err := os.WriteFile(path, []byte("include_usage: false\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if cfg.IncludeUsage {
		t.Fatalf("expected include_usage false override to win")
	}
}

func TestValidateRejectsInvalidContextWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{
			name: "zero context window",
			cfg: &Config{
				Endpoint:       "http://localhost:18080/v1",
				Listen:         "127.0.0.1:8400",
				ContextWindows: map[string]int{"zero-model": 0},
			},
			want: "context window",
		},
		{
			name: "negative context window",
			cfg: &Config{
				Endpoint:       "http://localhost:18080/v1",
				Listen:         "127.0.0.1:8400",
				ContextWindows: map[string]int{"negative-model": -1},
			},
			want: "context window",
		},
		{
			name: "negative default context window",
			cfg: &Config{
				Endpoint:             "http://localhost:18080/v1",
				Listen:               "127.0.0.1:8400",
				DefaultContextWindow: -1,
			},
			want: "default context window",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error to contain %q, got %v", tt.want, err)
			}
		})
	}
}

func TestSessionDataDirUsesConfiguredValue(t *testing.T) {
	cfg := &Config{DataDir: "/srv/pane/data"}

	dir, err := cfg.SessionDataDir()
	if err != nil {
		t.Fatalf("resolving data dir: %v", err)
	}
	if dir != "/srv/pane/data" {
		t.Fatalf("expected the configured value, got %q", dir)
	}
}

func TestSessionDataDirExpandsLeadingTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	cfg := &Config{DataDir: "~/pane-test"}

	dir, err := cfg.SessionDataDir()
	if err != nil {
		t.Fatalf("resolving data dir: %v", err)
	}
	if dir != filepath.Join(home, "pane-test") {
		t.Fatalf("expected the tilde expanded to the home directory, got %q", dir)
	}
}

func TestSessionDataDirLeavesEmbeddedTildeAlone(t *testing.T) {
	cfg := &Config{DataDir: "/srv/~backup/pane"}

	dir, err := cfg.SessionDataDir()
	if err != nil {
		t.Fatalf("resolving data dir: %v", err)
	}
	if dir != "/srv/~backup/pane" {
		t.Fatalf("expected the path untouched, got %q", dir)
	}
}

func TestSessionDataDirDefaultsToXDGDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	cfg := &Config{}

	dir, err := cfg.SessionDataDir()
	if err != nil {
		t.Fatalf("resolving data dir: %v", err)
	}
	if dir != filepath.Join("/xdg/data", "pane") {
		t.Fatalf("expected the XDG data home, got %q", dir)
	}
}

func TestSessionDataDirDefaultsToLocalShare(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	cfg := &Config{}

	dir, err := cfg.SessionDataDir()
	if err != nil {
		t.Fatalf("resolving data dir: %v", err)
	}
	if dir != filepath.Join(home, ".local", "share", "pane") {
		t.Fatalf("expected the local share default, got %q", dir)
	}
}

func TestLoadAcceptsDataDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	t.Chdir(tmp)

	path := filepath.Join(tmp, "pane.yaml")
	if err := os.WriteFile(path, []byte("data_dir: /srv/pane/data\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if cfg.DataDir != "/srv/pane/data" {
		t.Fatalf("expected data_dir bound from yaml, got %q", cfg.DataDir)
	}
}

func TestLoadAcceptsMaxTokens(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	t.Chdir(tmp)

	path := filepath.Join(tmp, "pane.yaml")
	content := "max_tokens:\n  qwen3.8-27b: 24756\ndefault_max_tokens: 8192\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if got := cfg.MaxTokens["qwen3.8-27b"]; got != 24756 {
		t.Fatalf("expected per-model max_tokens 24756, got %d", got)
	}
	if cfg.DefaultMaxTokens != 8192 {
		t.Fatalf("expected default_max_tokens 8192, got %d", cfg.DefaultMaxTokens)
	}
}

func TestLoadResolvesModelRegistry(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	t.Chdir(tmp)

	path := filepath.Join(tmp, "pane.yaml")
	content := `endpoint: http://shared.example/v1
api_key: shared-key
model: qwen@eleven
context_windows:
  qwen@unsecured: 999999
default_context_window: 888888
max_tokens:
  qwen@unsecured: 777777
default_max_tokens: 666666
models:
  qwen@eleven:
    upstream_model: qwen
    context_window: 262144
    max_tokens: 24756
  qwen@fortyfive:
    endpoint: http://fortyfive.example/v1
    api_key: fortyfive-key
    upstream_model: qwen
    context_window: 163840
    max_tokens: 12000
  qwen@unsecured:
    endpoint: http://localhost:8080/v1
    api_key: ""
    upstream_model: qwen
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if !cfg.HasModelRegistry() {
		t.Fatal("expected model registry mode")
	}

	eleven, ok := cfg.ResolveModel("qwen@eleven")
	if !ok {
		t.Fatal("expected inherited model to resolve")
	}
	if eleven.Endpoint != "http://shared.example/v1" || eleven.ApiKey != "shared-key" {
		t.Fatalf("expected shared connection fallback, got %#v", eleven)
	}
	if eleven.UpstreamModel != "qwen" || eleven.ContextWindow != 262144 || eleven.MaxTokens != 24756 {
		t.Fatalf("unexpected inherited profile: %#v", eleven)
	}

	fortyfive, ok := cfg.ResolveModel("qwen@fortyfive")
	if !ok {
		t.Fatal("expected override model to resolve")
	}
	if fortyfive.Endpoint != "http://fortyfive.example/v1" || fortyfive.ApiKey != "fortyfive-key" {
		t.Fatalf("expected profile connection override, got %#v", fortyfive)
	}

	unsecured, ok := cfg.ResolveModel("qwen@unsecured")
	if !ok {
		t.Fatal("expected unsecured model to resolve")
	}
	if unsecured.ApiKey != "" {
		t.Fatalf("expected explicit empty api key, got %q", unsecured.ApiKey)
	}
	if unsecured.ContextWindow != 0 || unsecured.MaxTokens != 0 {
		t.Fatalf("legacy token settings leaked into registry profile: %#v", unsecured)
	}

	if _, ok := cfg.ResolveModel("unknown"); ok {
		t.Fatal("expected unknown registry alias to be rejected")
	}
	models := cfg.ResolvedModels()
	if len(models) != 3 || models[0].Alias != "qwen@eleven" || models[1].Alias != "qwen@fortyfive" || models[2].Alias != "qwen@unsecured" {
		t.Fatalf("expected ordinal alias order, got %#v", models)
	}
}

func TestResolveLegacyModelKeepsExistingFallbacks(t *testing.T) {
	cfg := &Config{
		Endpoint:             "http://legacy.example/v1",
		ApiKey:               "legacy-key",
		Model:                "default-model",
		ContextWindows:       map[string]int{"override-model": 32768},
		DefaultContextWindow: 128000,
		MaxTokens:            map[string]int{"override-model": 4096},
		DefaultMaxTokens:     8192,
	}

	resolved, ok := cfg.ResolveModel("override-model")
	if !ok {
		t.Fatal("expected arbitrary legacy model to resolve")
	}
	if resolved.UpstreamModel != "override-model" || resolved.ContextWindow != 32768 || resolved.MaxTokens != 4096 {
		t.Fatalf("unexpected legacy override resolution: %#v", resolved)
	}

	resolved, ok = cfg.ResolveModel("")
	if !ok {
		t.Fatal("expected blank request to resolve the legacy default")
	}
	if resolved.Alias != "default-model" || resolved.ContextWindow != 128000 || resolved.MaxTokens != 8192 {
		t.Fatalf("unexpected legacy default resolution: %#v", resolved)
	}
}

func TestValidateRejectsInvalidModelRegistry(t *testing.T) {
	valid := &ModelConfig{Endpoint: "http://model.example/v1", UpstreamModel: "upstream"}

	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{
			name: "default alias missing",
			cfg: &Config{
				Listen: "127.0.0.1:8400",
				Model:  "missing",
				Models: map[string]*ModelConfig{"valid": valid},
			},
			want: "not in the model registry",
		},
		{
			name: "whitespace alias",
			cfg: &Config{
				Listen: "127.0.0.1:8400",
				Model:  "valid",
				Models: map[string]*ModelConfig{"valid": valid, "   ": valid},
			},
			want: "key must not be blank",
		},
		{
			name: "nil profile",
			cfg: &Config{
				Listen: "127.0.0.1:8400",
				Model:  "nil-model",
				Models: map[string]*ModelConfig{"nil-model": nil},
			},
			want: "configuration is required",
		},
		{
			name: "no effective endpoint",
			cfg: &Config{
				Listen: "127.0.0.1:8400",
				Model:  "model",
				Models: map[string]*ModelConfig{"model": {}},
			},
			want: "endpoint is required",
		},
		{
			name: "zero context window",
			cfg: &Config{
				Listen: "127.0.0.1:8400",
				Model:  "model",
				Models: map[string]*ModelConfig{"model": {
					Endpoint:      "http://model.example/v1",
					ContextWindow: intPointer(0),
				}},
			},
			want: "context window",
		},
		{
			name: "negative max tokens",
			cfg: &Config{
				Listen: "127.0.0.1:8400",
				Model:  "model",
				Models: map[string]*ModelConfig{"model": {
					Endpoint:  "http://model.example/v1",
					MaxTokens: intPointer(-1),
				}},
			},
			want: "max tokens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error to contain %q, got %v", tt.want, err)
			}
		})
	}
}

func intPointer(value int) *int {
	return &value
}
