package session

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	return store
}

func TestSaveGetRoundTripsBytesUnchanged(t *testing.T) {
	store := newTestStore(t)

	doc := []byte(`{"title":"a conversation","messages":[{"role":"user","content":"hi"}],"createdAt":1,"updatedAt":2}`)
	if err := store.Save("abc", doc); err != nil {
		t.Fatalf("saving: %v", err)
	}

	got, err := store.Get("abc")
	if err != nil {
		t.Fatalf("getting: %v", err)
	}
	if string(got) != string(doc) {
		t.Fatalf("expected the stored bytes unchanged, got %q", got)
	}
}

func TestSaveReplacesExistingDocument(t *testing.T) {
	store := newTestStore(t)

	if err := store.Save("abc", []byte(`{"title":"first","messages":[1,2,3,4,5,6,7,8,9,10]}`)); err != nil {
		t.Fatalf("saving: %v", err)
	}
	if err := store.Save("abc", []byte(`{"title":"second"}`)); err != nil {
		t.Fatalf("re-saving: %v", err)
	}

	got, err := store.Get("abc")
	if err != nil {
		t.Fatalf("getting: %v", err)
	}
	if string(got) != `{"title":"second"}` {
		t.Fatalf("expected the replacement document, got %q", got)
	}
}

func TestListProjectsCamelCaseFieldsAndSorts(t *testing.T) {
	store := newTestStore(t)

	docs := map[string]string{
		"older":  `{"title":"older","createdAt":1,"updatedAt":10}`,
		"newer":  `{"title":"newer","createdAt":2,"updatedAt":30}`,
		"tie-b":  `{"title":"tie b","createdAt":3,"updatedAt":20}`,
		"tie-a":  `{"title":"tie a","createdAt":4,"updatedAt":20}`,
		"titled": `{"createdAt":5,"updatedAt":5}`,
	}
	for id, doc := range docs {
		if err := store.Save(id, []byte(doc)); err != nil {
			t.Fatalf("saving '%s': %v", id, err)
		}
	}

	summaries, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}

	wantOrder := []string{"newer", "tie-a", "tie-b", "older", "titled"}
	if len(summaries) != len(wantOrder) {
		t.Fatalf("expected %d summaries, got %d", len(wantOrder), len(summaries))
	}
	for i, want := range wantOrder {
		if summaries[i].ID != want {
			t.Fatalf("expected summary %d to be '%s', got '%s'", i, want, summaries[i].ID)
		}
	}

	newest := summaries[0]
	if newest.Title != "newer" || newest.CreatedAt != 2 || newest.UpdatedAt != 30 {
		t.Fatalf("expected the projection filled from the body, got %+v", newest)
	}
}

func TestListProjectionRequiresCamelCaseKeys(t *testing.T) {
	store := newTestStore(t)

	if err := store.Save("camel", []byte(`{"title":"camel","createdAt":11,"updatedAt":22}`)); err != nil {
		t.Fatalf("saving: %v", err)
	}
	// dd's default name conversion would read these; the explicit camelCase
	// tags are what keeps the projection exact-key in both directions.
	if err := store.Save("snake", []byte(`{"title":"snake","created_at":11,"updated_at":22}`)); err != nil {
		t.Fatalf("saving: %v", err)
	}

	summaries, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}

	byID := map[string]Summary{}
	for _, summary := range summaries {
		byID[summary.ID] = summary
	}
	if byID["camel"].CreatedAt != 11 || byID["camel"].UpdatedAt != 22 {
		t.Fatalf("expected camelCase keys to populate the projection, got %+v", byID["camel"])
	}
	if byID["snake"].CreatedAt != 0 || byID["snake"].UpdatedAt != 0 {
		t.Fatalf("expected snake_case keys to be ignored, got %+v", byID["snake"])
	}
}

func TestListIgnoresBodyID(t *testing.T) {
	store := newTestStore(t)

	if err := store.Save("real", []byte(`{"id":"impostor","title":"t","updatedAt":1}`)); err != nil {
		t.Fatalf("saving: %v", err)
	}

	summaries, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "real" {
		t.Fatalf("expected the file's name to be the id, got %+v", summaries)
	}
}

func TestListEmptyStore(t *testing.T) {
	store := newTestStore(t)

	summaries, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if summaries == nil {
		t.Fatalf("expected an empty slice, got nil")
	}
	if len(summaries) != 0 {
		t.Fatalf("expected no summaries, got %d", len(summaries))
	}
}

func TestListSkipsUnusableFiles(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}

	if err := store.Save("good", []byte(`{"title":"good","updatedAt":1}`)); err != nil {
		t.Fatalf("saving: %v", err)
	}
	// a body that is not a JSON object, and a stem the id rule rejects: both
	// are skipped rather than breaking the rail.
	writeFile(t, filepath.Join(dir, "array.json"), `[1,2,3]`)
	writeFile(t, filepath.Join(dir, ".json"), `{"title":"nameless"}`)
	writeFile(t, filepath.Join(dir, "notjson.txt"), `{"title":"ignored"}`)
	writeFile(t, filepath.Join(dir, "tmp-12345"), `{"title":"debris"}`)

	summaries, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "good" {
		t.Fatalf("expected only the good file, got %+v", summaries)
	}
}

func TestGetMissingReportsNotExist(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.Get("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected a not-exist error, got %v", err)
	}
}

func TestGetCorruptDocumentReportsInvalid(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	writeFile(t, filepath.Join(dir, "damaged.json"), `{"title":"half`)

	_, err = store.Get("damaged")
	if !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("expected ErrInvalidDocument, got %v", err)
	}
	if !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("expected the error to name the id, got %v", err)
	}
}

func TestSaveRejectsInvalidDocuments(t *testing.T) {
	store := newTestStore(t)

	cases := map[string]string{
		"array top level":  `[1,2,3]`,
		"string top level": `"just a string"`,
		"null top level":   `null`,
		"duplicate keys":   `{"title":"a","title":"b"}`,
		"trailing data":    `{"title":"a"} {"title":"b"}`,
		"invalid json":     `{"title":`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			err := store.Save("abc", []byte(doc))
			if !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("expected ErrInvalidDocument, got %v", err)
			}
		})
	}
}

func TestSaveRejectsOversizedDocuments(t *testing.T) {
	store := newTestStore(t)

	padding := bytes.Repeat([]byte("x"), MaxDocumentSize)
	doc := append(append([]byte(`{"title":"`), padding...), []byte(`"}`)...)

	err := store.Save("abc", doc)
	if !errors.Is(err, ErrOversized) {
		t.Fatalf("expected ErrOversized, got %v", err)
	}
}

func TestUnsafeIDsAreRejected(t *testing.T) {
	store := newTestStore(t)

	for _, id := range []string{"", ".", "..", "../escape", "sub/dir", "trailing/", "./here"} {
		t.Run(fmt.Sprintf("id %q", id), func(t *testing.T) {
			if _, err := store.Get(id); !errors.Is(err, ErrUnsafeID) {
				t.Fatalf("Get: expected ErrUnsafeID, got %v", err)
			}
			if err := store.Save(id, []byte(`{}`)); !errors.Is(err, ErrUnsafeID) {
				t.Fatalf("Save: expected ErrUnsafeID, got %v", err)
			}
			if _, err := store.Delete(id); !errors.Is(err, ErrUnsafeID) {
				t.Fatalf("Delete: expected ErrUnsafeID, got %v", err)
			}
		})
	}
}

func TestSaveEscapeAttemptWritesNothingOutsideTheStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}

	if err := store.Save("../escaped", []byte(`{"title":"escaped"}`)); !errors.Is(err, ErrUnsafeID) {
		t.Fatalf("expected ErrUnsafeID, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected nothing written outside the store, got %v", err)
	}
}

func TestDeleteRemovesAndReportsAbsence(t *testing.T) {
	store := newTestStore(t)

	if err := store.Save("abc", []byte(`{"title":"a"}`)); err != nil {
		t.Fatalf("saving: %v", err)
	}

	removed, err := store.Delete("abc")
	if err != nil {
		t.Fatalf("deleting: %v", err)
	}
	if !removed {
		t.Fatalf("expected the delete to report removal")
	}

	removed, err = store.Delete("abc")
	if err != nil {
		t.Fatalf("deleting a missing id reported an error: %v", err)
	}
	if removed {
		t.Fatalf("expected a missing id to report false")
	}
}

func TestNewStoreFailsOnUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write into a read-only directory")
	}

	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")
	if err := os.Mkdir(sessions, 0o500); err != nil {
		t.Fatalf("creating read-only directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sessions, 0o700) })

	// MkdirAll succeeds on the existing directory; the write probe is what
	// catches a directory the process cannot create files in.
	if _, err := NewStore(sessions); err == nil {
		t.Fatalf("expected NewStore to fail on a read-only directory")
	}
}

func TestSaveReportsOperationalFailureAsPlainError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write into a read-only directory")
	}

	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err = store.Save("abc", []byte(`{"title":"a"}`))
	if err == nil {
		t.Fatalf("expected the save to fail")
	}
	// an operational failure is deliberately none of the client-error
	// sentinels: the api maps it to a 500, never a 4xx.
	if errors.Is(err, ErrUnsafeID) || errors.Is(err, ErrInvalidDocument) || errors.Is(err, ErrOversized) {
		t.Fatalf("expected a plain operational error, got %v", err)
	}
}

func TestSaveLeavesNoDebrisOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write into a read-only directory")
	}

	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	// a directory whose entries cannot be replaced: the temp file is created
	// under the store's own handle, the rename fails, and the fragment must
	// still be cleaned up.
	if err := store.Save("abc", []byte(`{"title":"a"}`)); err != nil {
		t.Fatalf("saving: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "blocked.json"), 0o700); err != nil {
		t.Fatalf("creating a blocking directory: %v", err)
	}

	if err := store.Save("blocked", []byte(`{"title":"blocked"}`)); err == nil {
		t.Fatalf("expected the save to fail")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "tmp-") {
			t.Fatalf("expected no temp debris, found '%s'", entry.Name())
		}
	}
}

func TestConcurrentOperationsAreSerialized(t *testing.T) {
	store := newTestStore(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("conv-%d", i)
			for round := 0; round < 20; round++ {
				doc := fmt.Appendf(nil, `{"title":"conv %d","updatedAt":%d}`, i, round)
				if err := store.Save(id, doc); err != nil {
					t.Errorf("saving '%s': %v", id, err)
					return
				}
				if _, err := store.List(); err != nil {
					t.Errorf("listing: %v", err)
					return
				}
				if _, err := store.Get(id); err != nil {
					t.Errorf("getting '%s': %v", id, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	summaries, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(summaries) != 8 {
		t.Fatalf("expected 8 summaries, got %d", len(summaries))
	}
}

func TestRenamedFileIsServedUnderItsNewName(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}

	doc := []byte(`{"title":"renamed","updatedAt":7}`)
	if err := store.Save("abc", doc); err != nil {
		t.Fatalf("saving: %v", err)
	}
	if err := os.Rename(filepath.Join(dir, "abc.json"), filepath.Join(dir, "zed.json")); err != nil {
		t.Fatalf("renaming: %v", err)
	}

	summaries, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "zed" {
		t.Fatalf("expected the conversation under its new name, got %+v", summaries)
	}

	got, err := store.Get("zed")
	if err != nil {
		t.Fatalf("getting 'zed': %v", err)
	}
	if string(got) != string(doc) {
		t.Fatalf("expected the same bytes under the new name, got %q", got)
	}
	if _, err := store.Get("abc"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected the old name to be gone, got %v", err)
	}
}

func TestURLReservedButSafeNameRoundTrips(t *testing.T) {
	store := newTestStore(t)

	// '#' is legal on disk and illegal in an unencoded URL: the store accepts
	// it, and the frontend adapter is what percent-encodes the id.
	const id = "issue#1"
	doc := []byte(`{"title":"issue 1","updatedAt":3}`)
	if err := store.Save(id, doc); err != nil {
		t.Fatalf("saving: %v", err)
	}

	summaries, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != id {
		t.Fatalf("expected '%s' listed, got %+v", id, summaries)
	}

	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("getting: %v", err)
	}
	if string(got) != string(doc) {
		t.Fatalf("expected the same bytes, got %q", got)
	}

	removed, err := store.Delete(id)
	if err != nil {
		t.Fatalf("deleting: %v", err)
	}
	if !removed {
		t.Fatalf("expected the delete to report removal")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing '%s': %v", path, err)
	}
}
