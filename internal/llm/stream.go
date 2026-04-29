package llm

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var errStreamClosedBeforeDone = errors.New("upstream stream closed before '[DONE]'")

// StreamReader reads OpenAI-compatible SSE streaming responses.
type StreamReader struct {
	body   io.ReadCloser
	reader *bufio.Reader
	done   bool
}

func NewStreamReader(body io.ReadCloser) *StreamReader {
	return &StreamReader{
		body:   body,
		reader: bufio.NewReader(body),
	}
}

// Recv reads the next chunk from the stream.
// returns io.EOF when the stream is complete ([DONE] received).
func (s *StreamReader) Recv() (*StreamChunk, error) {
	if s.done {
		return nil, io.EOF
	}

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("reading stream: %w", err)
		}
		if line == "" && err == io.EOF {
			return nil, errStreamClosedBeforeDone
		}

		line = strings.TrimSpace(line)

		if line == "" {
			if err == io.EOF {
				return nil, errStreamClosedBeforeDone
			}
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			if err == io.EOF {
				return nil, errStreamClosedBeforeDone
			}
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			s.done = true
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
