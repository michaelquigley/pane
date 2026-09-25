package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func testCredential() *Credential {
	return &Credential{Access: "access-canary", Refresh: "refresh-canary", AccountID: "acct-pane-test-a", ExpiresAt: time.Now().Add(time.Hour)}
}

func TestPrivateStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pane")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory permissions: %v %v", info, err)
	}
	if _, err := s.Read(); !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("missing credential: %v", err)
	}
	if err := s.Save(context.Background(), testCredential()); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(s.path()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file permissions: %v %v", info, err)
	}
	got, err := s.Read()
	if err != nil || got.AccountID != "acct-pane-test-a" {
		t.Fatalf("read: %v %v", got, err)
	}
	if err := s.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(); !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("logout: %v", err)
	}
	if err := s.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCancelledSaveBeforeLockDoesNotWrite(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "pane"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), testCredential()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.lockPath()); err != nil {
		t.Fatal(err)
	}
	candidate := testCredential()
	candidate.Access = "replacement-access"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Save(ctx, candidate); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save: %v", err)
	}
	if _, err := os.Stat(store.lockPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled save attempted lock: %v", err)
	}
	got, err := store.Read()
	if err != nil || got.Access != "access-canary" {
		t.Fatalf("cancelled save changed credential: %v %v", got, err)
	}
	if err := store.Save(context.Background(), candidate); err != nil {
		t.Fatalf("lock unusable afterward: %v", err)
	}
	got, err = store.Read()
	if err != nil || got.Access != candidate.Access {
		t.Fatalf("subsequent save failed: %v %v", got, err)
	}
}

type waitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

type cancelAfterAcquisitionContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelAfterAcquisitionContext) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestCancelledSaveAfterLockAcquisitionReleasesLock(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "pane"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), testCredential()); err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterAcquisitionContext{Context: base, cancel: cancel}
	candidate := testCredential()
	candidate.Access = "replacement-access"
	if err := store.Save(ctx, candidate); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-acquisition cancellation: %v", err)
	}
	if ctx.checks != 2 {
		t.Fatalf("expected checks before and after lock, got %d", ctx.checks)
	}
	got, err := store.Read()
	if err != nil || got.Access != "access-canary" {
		t.Fatalf("post-acquisition cancellation changed credential: %v %v", got, err)
	}
	if err := store.Save(context.Background(), candidate); err != nil {
		t.Fatalf("lock not released: %v", err)
	}
}

func (c *waitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestCancelledSaveWhileWaitingForLockDoesNotWrite(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "pane"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), testCredential()); err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- store.withLock(context.Background(), func() error { close(held); <-release; return nil })
	}()
	select {
	case <-held:
	case <-time.After(time.Second):
		t.Fatal("holder did not acquire lock")
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &waitingContext{Context: ctx, waiting: make(chan struct{})}
	candidate := testCredential()
	candidate.Access = "replacement-access"
	waiterDone := make(chan error, 1)
	go func() { waiterDone <- store.Save(observed, candidate) }()
	select {
	case <-observed.waiting:
	case <-time.After(time.Second):
		t.Fatal("save did not wait for held lock")
	}
	cancel()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting save: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled save did not return")
	}
	got, err := store.Read()
	if err != nil || got.Access != "access-canary" {
		t.Fatalf("waiting save changed credential: %v %v", got, err)
	}
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), candidate); err != nil {
		t.Fatalf("lock unusable afterward: %v", err)
	}
	got, err = store.Read()
	if err != nil || got.Access != candidate.Access {
		t.Fatalf("subsequent save failed: %v %v", got, err)
	}
}

func TestPrivateStoreRejectsUnsafePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	base := t.TempDir()
	open := func(name string) error { _, err := OpenStore(filepath.Join(base, name)); return err }
	if err := os.Mkdir(filepath.Join(base, "loose"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(base, "loose"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := open("loose"); err == nil {
		t.Fatal("accepted loose directory")
	}
	if err := os.Symlink(filepath.Join(base, "loose"), filepath.Join(base, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := open("linked"); err == nil {
		t.Fatal("accepted linked directory")
	}
	for _, name := range []string{"auth.json", "auth.lock"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(base, strings.ReplaceAll(name, ".", "-"))
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("unsafe"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenStore(dir); err == nil {
				t.Fatal("accepted loose auth file")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(base, "target"), path); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenStore(dir); err == nil {
				t.Fatal("accepted auth symlink")
			}
			_ = s
		})
	}
}

func TestPrivateStoreCorruptionAndCanary(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "pane"))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"{", `{"access":"canary"}`, `{"access":"a","refresh":"b","account_id":"c","expires_at":"2030-01-01T00:00:00Z"} {}`} {
		if err := os.WriteFile(s.path(), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := s.Read()
		if err == nil || strings.Contains(fmt.Sprint(err), "canary") {
			t.Fatalf("corrupt store exposed data: %v", err)
		}
	}
	if got := fmt.Sprintf("%v %#v", testCredential(), testCredential()); strings.Contains(got, "canary") {
		t.Fatalf("credential formatting leaked: %s", got)
	}
}

func TestGlobalDirRespectsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	dir, err := GlobalDir()
	if err != nil || dir != filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "pane") {
		t.Fatalf("global dir: %q %v", dir, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := GlobalDir(); err == nil {
		t.Fatal("accepted relative XDG_CONFIG_HOME")
	}
}

func TestAccountScopeVectors(t *testing.T) {
	for id, want := range map[string]string{"acct-pane-test-a": "chatgpt:ea1e30f202148af5945f8c85", "acct-pane-test-b": "chatgpt:3847545a9761c55a4f3fe66b"} {
		if got := AccountScope(id); got != want {
			t.Fatalf("%s: %s", id, got)
		}
	}
}
