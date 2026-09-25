// Package auth is the spike's private subscription credential manager: a
// scratch store with owner-only permissions, a cross-process lock, atomic
// writes, and double-checked refresh. it never reads pi or codex stores.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Credential is the stored subscription login. it is never logged; String
// is redacted.
type Credential struct {
	Access    string    `json:"access"`
	Refresh   string    `json:"refresh"`
	ExpiresAt time.Time `json:"expires_at"`
	AccountID string    `json:"account_id"`
}

func (c Credential) String() string { return "credential(redacted)" }

// ErrLoginRequired means no credential, or one the provider rejected.
var ErrLoginRequired = errors.New("login required")

// Store is a file-backed credential store in a private scratch directory.
type Store struct {
	dir string
}

// forbiddenStores are locations the spike must never touch.
func forbiddenStores() []string {
	home, _ := os.UserHomeDir()
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	return []string{
		filepath.Join(home, ".codex"),
		filepath.Join(home, ".pi"),
		filepath.Join(cfg, "pane"), // pane's eventual production auth store
		filepath.Join(cfg, "pi"),
	}
}

// OpenStore opens (creating 0700) the scratch directory, refusing pi/codex
// and pane production locations.
func OpenStore(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		abs = filepath.Join(resolved, filepath.Base(abs))
	}
	for _, f := range forbiddenStores() {
		if abs == f || strings.HasPrefix(abs, f+string(os.PathSeparator)) {
			return nil, fmt.Errorf("refusing credential store inside '%s'", f)
		}
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: abs}, nil
}

func (s *Store) path() string     { return filepath.Join(s.dir, "openai-auth.json") }
func (s *Store) lockPath() string { return filepath.Join(s.dir, "openai-auth.lock") }

// Dir reports the store directory (not its contents).
func (s *Store) Dir() string { return s.dir }

// withLock holds an exclusive cross-process flock for fn.
func (s *Store) withLock(ctx context.Context, fn func() error) error {
	f, err := os.OpenFile(s.lockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// Read returns the stored credential or ErrLoginRequired.
func (s *Store) Read() (*Credential, error) {
	b, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrLoginRequired
	}
	if err != nil {
		return nil, err
	}
	var c Credential
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("credential store is corrupt: %w", err)
	}
	if c.Access == "" || c.Refresh == "" {
		return nil, ErrLoginRequired
	}
	return &c, nil
}

// write atomically replaces the credential (temp file, fsync, rename).
// callers hold the lock.
func (s *Store) write(c *Credential) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".openai-auth-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.path()); err != nil {
		return err
	}
	d, err := os.Open(s.dir)
	if err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// Save stores a fresh login under the lock.
func (s *Store) Save(ctx context.Context, c *Credential) error {
	return s.withLock(ctx, func() error { return s.write(c) })
}

// Logout removes local credentials only; it revokes nothing upstream.
func (s *Store) Logout(ctx context.Context) error {
	return s.withLock(ctx, func() error {
		err := os.Remove(s.path())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
}

// AccountIDFromJWT reads the chatgpt account id claim without verifying the
// signature: the token came from the token endpoint over TLS.
func AccountIDFromJWT(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("access token is not a jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", errors.New("access token payload is not base64url")
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", errors.New("access token payload is not json")
	}
	var auth struct {
		AccountID string `json:"chatgpt_account_id"`
	}
	if raw, ok := claims["https://api.openai.com/auth"]; ok {
		_ = json.Unmarshal(raw, &auth)
	}
	if auth.AccountID == "" {
		return "", errors.New("access token has no chatgpt account id claim")
	}
	return auth.AccountID, nil
}

// AccountScope derives the non-secret replay binding from an account id. it
// is a salted hash so stored conversations do not carry the raw id.
func AccountScope(accountID string) string {
	sum := sha256.Sum256([]byte("pane-account-scope-v1:" + accountID))
	return "chatgpt:" + hex.EncodeToString(sum[:12])
}
