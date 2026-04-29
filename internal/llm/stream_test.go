package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestStreamReaderReturnsEOFOnlyAfterDone(t *testing.T) {
	t.Parallel()

	reader := NewStreamReader(newStreamBody(
		streamDataLine(t, StreamChunk{
			ID: "chat-1",
			Choices: []Choice{{
				Index: 0,
				Delta: Delta{
					Content: StringContent("hello"),
				},
			}},
		}) + "\n" +
			"data: [DONE]\n\n",
	))

	chunk, err := reader.Recv()
	if err != nil {
		t.Fatalf("receiving stream chunk: %v", err)
	}
	if chunk.ID != "chat-1" {
		t.Fatalf("expected chunk id 'chat-1', got %q", chunk.ID)
	}
	if len(chunk.Choices) != 1 || chunk.Choices[0].Delta.Content == nil || *chunk.Choices[0].Delta.Content != "hello" {
		t.Fatalf("unexpected chunk content: %#v", chunk)
	}

	if _, err := reader.Recv(); err != io.EOF {
		t.Fatalf("expected EOF after [DONE], got %v", err)
	}
	if _, err := reader.Recv(); err != io.EOF {
		t.Fatalf("expected repeated EOF after [DONE], got %v", err)
	}
}

func TestStreamReaderErrorsWhenBodyEndsBeforeDone(t *testing.T) {
	t.Parallel()

	reader := NewStreamReader(newStreamBody(""))

	_, err := reader.Recv()
	if !errors.Is(err, errStreamClosedBeforeDone) {
		t.Fatalf("expected stream closed before done error, got %v", err)
	}
}

func TestStreamReaderErrorsAfterChunkWithoutDone(t *testing.T) {
	t.Parallel()

	reader := NewStreamReader(newStreamBody(streamDataLine(t, StreamChunk{
		ID: "chat-1",
		Choices: []Choice{{
			Index: 0,
			Delta: Delta{
				Content: StringContent("partial"),
			},
		}},
	}) + "\n"))

	chunk, err := reader.Recv()
	if err != nil {
		t.Fatalf("receiving stream chunk: %v", err)
	}
	if chunk.Choices[0].Delta.Content == nil || *chunk.Choices[0].Delta.Content != "partial" {
		t.Fatalf("unexpected chunk content: %#v", chunk)
	}

	_, err = reader.Recv()
	if !errors.Is(err, errStreamClosedBeforeDone) {
		t.Fatalf("expected stream closed before done error, got %v", err)
	}
}

func newStreamBody(body string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(body))
}

func streamDataLine(t *testing.T, chunk StreamChunk) string {
	t.Helper()

	data, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("marshaling stream chunk: %v", err)
	}
	return fmt.Sprintf("data: %s\n", data)
}
