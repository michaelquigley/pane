package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michaelquigley/pane/internal/auth"
)

func TestAuthCommandsIgnoreUnrelatedConfigAndStorage(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Chdir(tmp)
	if err := os.WriteFile("pane.yaml", []byte("models: [invalid]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args []string
		want string
	}{{[]string{"status", "openai"}, "login required"}, {[]string{"logout", "openai"}, "removed pane's local"}, {[]string{"login", "wrong"}, "expected provider"}, {[]string{"login", "openai", "--method", "device"}, "unknown flag: --method"}, {[]string{"login", "openai", "--method", "browser"}, "unknown flag: --method"}} {
		cmd := newAuthCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(tt.args)
		err := cmd.Execute()
		if !strings.Contains(out.String(), tt.want) && (err == nil || !strings.Contains(err.Error(), tt.want)) {
			t.Fatalf("%v: output %q error %v", tt.args, out.String(), err)
		}
	}
	if _, err := os.Stat(filepath.Join(tmp, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("auth touched conversation storage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "config", "pane", "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth created credential on status/logout: %v", err)
	}
	store, err := auth.OpenGlobalStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(t.Context(), &auth.Credential{Access: "access-canary", AccountID: "acct-pane-test-a"}); err == nil {
		t.Fatal("accepted incomplete credential")
	}
}

func TestAuthCLIHelper(t *testing.T) {
	if os.Getenv("PANE_AUTH_CLI_CHILD") != "1" {
		return
	}
	rootCmd.SetArgs([]string{"auth", "status", "openai"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthCLIStatusDoesNotPrintSecrets(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	store, err := auth.OpenGlobalStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(t.Context(), &auth.Credential{Access: "access-canary-strong", AccountID: "account-canary-strong", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestAuthCLIHelper$")
	cmd.Env = append(os.Environ(), "PANE_AUTH_CLI_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status process: %v: %s", err, out)
	}
	for _, secret := range []string{"access-canary-strong", "account-canary-strong"} {
		if bytes.Contains(out, []byte(secret)) {
			t.Fatalf("credential appeared in cli output")
		}
	}
	if !bytes.Contains(out, []byte("openai: signed in")) {
		t.Fatalf("missing safe status: %s", out)
	}
}

// the single login command is the device flow: it offers no method selector
// and describes device-code sign-in.
func TestLoginIsDeviceOnly(t *testing.T) {
	login, _, err := newAuthCommand().Find([]string{"login"})
	if err != nil {
		t.Fatal(err)
	}
	if login.Flags().Lookup("method") != nil || !strings.Contains(login.Short, "device code") {
		t.Fatalf("login command: short=%q", login.Short)
	}
}

func TestAuthStatusReportsExpiredLogin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store, err := auth.OpenGlobalStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(t.Context(), &auth.Credential{Access: "access", AccountID: "acct-pane-test-a", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	cmd := newAuthCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"status", "openai"})
	if err := cmd.Execute(); err != nil || !strings.Contains(out.String(), "login expired; run 'pane auth login openai'") {
		t.Fatalf("expired status: %q %v", out.String(), err)
	}
}
