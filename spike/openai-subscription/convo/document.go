// Package convo is the spike's stand-in for pane's session document: the
// portable messages (with origin and continuation) plus the recovery record,
// serialized as one opaque JSON file.
package convo

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/michaelquigley/pane/spike/openai-subscription/loop"
	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

// Document is one conversation.
type Document struct {
	Title    string            `json:"title"`
	Messages []round.Message   `json:"messages"`
	Turns    []loop.TurnRecord `json:"turns,omitempty"`
}

// Save writes atomically with owner-only permissions: real continuation is
// account-bound and stays private.
func Save(path string, d *Document) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".doc-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
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
	return os.Rename(tmp.Name(), path)
}

// Load reads a document.
func Load(path string) (*Document, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d Document
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// FileRecorder persists the active turn record into the document after each
// change -- the harness's crash-visible incremental record.
type FileRecorder struct {
	Path string
	Doc  *Document
	slot int
}

// NewFileRecorder appends a turn slot to doc.
func NewFileRecorder(path string, doc *Document) *FileRecorder {
	doc.Turns = append(doc.Turns, loop.TurnRecord{})
	return &FileRecorder{Path: path, Doc: doc, slot: len(doc.Turns) - 1}
}

func (f *FileRecorder) Record(rec *loop.TurnRecord) error {
	f.Doc.Turns[f.slot] = *rec
	return Save(f.Path, f.Doc)
}
