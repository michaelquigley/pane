package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/michaelquigley/df/dd"
)

var ErrLoginRequired = errors.New("login required")

type Credential struct {
	Access    string
	Refresh   string
	ExpiresAt time.Time
	AccountID string
}

func (Credential) String() string   { return "credential(redacted)" }
func (Credential) GoString() string { return "credential(redacted)" }

type Store struct{ dir string }

func GlobalDir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home directory: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(base) {
		return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
	}
	return filepath.Join(base, "pane"), nil
}

func OpenGlobalStore() (*Store, error) {
	dir, err := GlobalDir()
	if err != nil {
		return nil, err
	}
	return OpenStore(dir)
}

// the store keeps all pane credentials under one private directory.
func OpenStore(dir string) (*Store, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("auth directory must be an absolute path")
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return nil, fmt.Errorf("creating config parent: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("creating auth directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("checking auth directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("auth directory must not be writable by others or be a symlink")
	}
	s := &Store{dir: dir}
	for _, path := range []string{s.path(), s.lockPath()} {
		if err := checkPrivateFile(path); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) path() string     { return filepath.Join(s.dir, "auth.json") }
func (s *Store) lockPath() string { return filepath.Join(s.dir, "auth.lock") }

func checkPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking private auth file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("auth file must be a private regular file (mode 0600 or stricter), not a symlink")
	}
	return nil
}

func (s *Store) Read() (*Credential, error) {
	if err := checkPrivateFile(s.path()); err != nil {
		return nil, err
	}
	f, err := openPrivateRead(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrLoginRequired
	}
	if err != nil {
		return nil, fmt.Errorf("reading auth file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("auth file must be a private regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil {
		return nil, fmt.Errorf("reading auth file: %w", err)
	}
	if len(b) > 64*1024 {
		return nil, errors.New("auth file is corrupt or too large")
	}
	var c Credential
	if err := dd.BindJSON(&c, b, dd.Strict()); err != nil {
		return nil, errors.New("auth file is corrupt")
	}
	if c.Access == "" || c.Refresh == "" || c.AccountID == "" || c.ExpiresAt.IsZero() {
		return nil, errors.New("auth file is corrupt")
	}
	return &c, nil
}

func (s *Store) withLock(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkPrivateFile(s.lockPath()); err != nil {
		return err
	}
	f, err := openPrivateLock(s.lockPath())
	if err != nil {
		return fmt.Errorf("opening auth lock: %w", err)
	}
	defer f.Close()
	if err := lock(ctx, f); err != nil {
		return err
	}
	defer unlock(f)
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

func (s *Store) write(c *Credential) error {
	if err := checkPrivateFile(s.path()); err != nil {
		return err
	}
	b, err := dd.UnbindJSON(c)
	if err != nil {
		return errors.New("encoding credential failed")
	}
	f, err := os.CreateTemp(s.dir, ".auth-*.tmp")
	if err != nil {
		return fmt.Errorf("creating auth file: %w", err)
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.path()); err != nil {
		return err
	}
	return syncDir(s.dir)
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (s *Store) Save(ctx context.Context, c *Credential) error {
	if c == nil || c.Access == "" || c.Refresh == "" || c.AccountID == "" || c.ExpiresAt.IsZero() {
		return errors.New("incomplete credential")
	}
	return s.withLock(ctx, func() error { return s.write(c) })
}

func (s *Store) Logout(ctx context.Context) error {
	return s.withLock(ctx, func() error {
		if err := checkPrivateFile(s.path()); err != nil {
			return err
		}
		err := os.Remove(s.path())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return syncDir(s.dir)
	})
}

func AccountIDFromJWT(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("access token is not a jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", errors.New("access token payload is invalid")
	}
	var claims struct {
		Auth struct {
			AccountID string `dd:"chatgpt_account_id"`
		} `dd:"https://api.openai.com/auth"`
	}
	if dd.BindJSON(&claims, payload) != nil {
		return "", errors.New("access token payload is invalid")
	}
	if claims.Auth.AccountID == "" {
		return "", errors.New("access token has no chatgpt account id")
	}
	return claims.Auth.AccountID, nil
}

func AccountScope(accountID string) string {
	if accountID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("pane-account-scope-v1:" + accountID))
	return "chatgpt:" + hex.EncodeToString(sum[:12])
}
