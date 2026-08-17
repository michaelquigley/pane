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
