package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigIncludesUsage(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()

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
