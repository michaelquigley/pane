package llm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// StreamReader reads OpenAI-compatible SSE streaming responses.
type StreamReader struct {
	body   io.ReadCloser
	reader *bufio.Reader
}

func NewStreamReader(body io.ReadCloser) *StreamReader {
	return &StreamReader{
		body:   body,
		reader: bufio.NewReader(body),
	}
}

// Recv reads the next chunk from the stream.
// Returns io.EOF when the stream is complete ([DONE] received).
func (s *StreamReader) Recv() (*StreamChunk, error) {
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("reading stream: %w", err)
		}

		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			return nil, io.EOF
		}

		var chunk StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("decoding stream chunk: %w", err)
		}

		return &chunk, nil
	}
}

func (s *StreamReader) Close() error {
	return s.body.Close()
}
