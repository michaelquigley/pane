// Package session is a file-backed document store for conversations: one
// opaque JSON file per conversation, named for the conversation's id. the
// store never interprets a document's content beyond the projection the rail
// needs; the id is the file's name, not a field of the body.
package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/df/dl"
)

// MaxDocumentSize is the largest document body the store accepts.
const MaxDocumentSize = 32 << 20

var (
	// ErrUnsafeID reports an id that does not name a single file inside the
	// store's directory.
	ErrUnsafeID = errors.New("unsafe session id")
	// ErrInvalidDocument reports a body that is not a well-formed JSON object.
	ErrInvalidDocument = errors.New("invalid session document")
	// ErrOversized reports a body over MaxDocumentSize.
	ErrOversized = errors.New("session document too large")
)

// tempPattern names the store's in-flight write fragments. it deliberately
// carries no '.json' suffix so List never reads one.
const tempPattern = "tmp-*"

// Summary is the rail's projection of a stored conversation: the id is the
// file's name, the rest is bound from the body's camelCase content fields.
type Summary struct {
	ID        string
	Title     string
	CreatedAt int64 `dd:"createdAt"`
	UpdatedAt int64 `dd:"updatedAt"`
}

// document is the shape a body validates against: no declared fields, so the
// '+extra' map is the declared home for everything the store does not
// interpret. binding a body to it under strict intake is exactly the check
// "is this a well-formed JSON object".
type document struct {
	Extra map[string]any `dd:",+extra"`
}

// Store is a directory of conversation documents. a per-store mutex serializes
// every file operation; one binary, one process, no locking protocol.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore creates the store's directory and proves the process can write
// inside it. the write probe is required because MkdirAll alone succeeds on an
// existing directory the process cannot create files in, and a store that
// starts clean and then fails on its first save would defeat the fail-fast
// promise. called once at startup; a failure here is fatal.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating session directory '%s': %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return nil, fmt.Errorf("session directory '%s' is not writable: %w", dir, err)
	}
	probePath := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(probePath)
		return nil, fmt.Errorf("session directory '%s' is not writable: %w", dir, err)
	}
	if err := os.Remove(probePath); err != nil {
		return nil, fmt.Errorf("session directory '%s' is not writable: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// List reads the projection of every stored document, sorted by updated
// descending with the id ordinal-ascending as the tiebreak. a file that cannot
// be read, whose body is not a JSON object, whose projection will not bind, or
// whose name stem is not a safe id is skipped with a warning: one bad file
// never breaks the rail, and the id rule keeps the rail to exactly the set of
// ids the item endpoints can address.
func (s *Store) List() ([]Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("reading session directory '%s': %w", s.dir, err)
	}

	summaries := []Summary{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if _, err := s.safeID(id); err != nil {
			dl.Warnf("skipping session file '%s': %v", name, err)
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			dl.Warnf("skipping session file '%s': %v", name, err)
			continue
		}
		if err := validateDocument(data); err != nil {
			dl.Warnf("skipping session file '%s': %v", name, err)
			continue
		}
		var summary Summary
		if err := dd.BindJSON(&summary, data); err != nil {
			dl.Warnf("skipping session file '%s': %v", name, err)
			continue
		}
		// the id is the name; a body that carries one of its own does not
		// get to name the conversation.
		summary.ID = id
		summaries = append(summaries, summary)
	}

	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].UpdatedAt != summaries[j].UpdatedAt {
			return summaries[i].UpdatedAt > summaries[j].UpdatedAt
		}
		return summaries[i].ID < summaries[j].ID
	})
	return summaries, nil
}

// Get returns the stored bytes for an id, unchanged. a stored body that is no
// longer a JSON object (damaged out of band) fails with ErrInvalidDocument
// naming the id.
func (s *Store) Get(id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.safeID(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading session '%s': %w", id, err)
	}
	if err := validateDocument(data); err != nil {
		return nil, fmt.Errorf("session '%s': %w: %v", id, ErrInvalidDocument, err)
	}
	return data, nil
}

// Save writes a document under an id, replacing whatever was there. the write
// is a temp file plus a rename, so a concurrent List or Delete never observes
// a partial document: os.CreateTemp creates at 0600 and the rename preserves
// that mode, so the document is owner-readable from its first byte.
func (s *Store) Save(id string, doc []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.safeID(id)
	if err != nil {
		return err
	}
	if len(doc) > MaxDocumentSize {
		return fmt.Errorf("session '%s' is %d bytes: %w", id, len(doc), ErrOversized)
	}
	if err := validateDocument(doc); err != nil {
		return fmt.Errorf("session '%s': %w: %v", id, ErrInvalidDocument, err)
	}

	temp, err := os.CreateTemp(s.dir, tempPattern)
	if err != nil {
		return fmt.Errorf("creating temp file for session '%s': %w", id, err)
	}
	tempPath := temp.Name()
	// every failure path between here and the rename takes the fragment with
	// it; after a successful rename the remove is a no-op on a name that no
	// longer exists.
	defer func() { _ = os.Remove(tempPath) }()

	if _, err := temp.Write(doc); err != nil {
		_ = temp.Close()
		return fmt.Errorf("writing session '%s': %w", id, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("writing session '%s': %w", id, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replacing session '%s': %w", id, err)
	}
	return nil
}

// Delete removes the document for an id. an id with no file reports false with
// no error.
func (s *Store) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.safeID(id)
	if err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("removing session '%s': %w", id, err)
	}
	return true, nil
}

// safeID applies the store's id rule and returns the path the id addresses.
// the rule is defense against a URL segment the mux already keeps slash-free;
// the store does not trust it.
func (s *Store) safeID(id string) (string, error) {
	if id == "" || id == "." || id == ".." {
		return "", fmt.Errorf("%w: '%s'", ErrUnsafeID, id)
	}
	if strings.ContainsRune(id, '/') || strings.ContainsRune(id, os.PathSeparator) {
		return "", fmt.Errorf("%w: '%s'", ErrUnsafeID, id)
	}
	if filepath.Clean(id) != id {
		return "", fmt.Errorf("%w: '%s'", ErrUnsafeID, id)
	}
	path := filepath.Join(s.dir, id+".json")
	if filepath.Dir(path) != filepath.Clean(s.dir) {
		return "", fmt.Errorf("%w: '%s'", ErrUnsafeID, id)
	}
	return path, nil
}

// validateDocument is the store's single definition of a valid document, used
// on the way in and on the way out: strict intake rejects a non-object top
// level, duplicate keys, and trailing data, and a well-formed JSON object is a
// valid document -- the id is the path's key, not a field of the body, so
// there is nothing else to check. a stored file that fails it was damaged out
// of band, since Save would never have written it.
func validateDocument(doc []byte) error {
	var d document
	return dd.BindJSON(&d, doc, dd.Strict())
}
