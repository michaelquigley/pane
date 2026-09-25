package llm

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/michaelquigley/df/dd"
)

var errStreamClosedBeforeDone = errors.New("upstream stream closed before '[DONE]'")
var errStreamBudgetExceeded = errors.New("upstream round exceeds 32 MiB")

const maxStreamRoundBytes = 32 * 1024 * 1024

// StreamReader reads OpenAI-compatible SSE streaming responses.
type StreamReader struct {
	body   io.ReadCloser
	reader *bufio.Reader
	limit  *io.LimitedReader
	done   bool
}

func NewStreamReader(body io.ReadCloser) *StreamReader {
	limit := &io.LimitedReader{R: body, N: maxStreamRoundBytes + 1}
	return &StreamReader{
		body:   body,
		reader: bufio.NewReader(limit),
		limit:  limit,
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
		if s.limit.N == 0 {
			return nil, errStreamBudgetExceeded
		}
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

		decoded, err := dd.DecodeStrictJSON([]byte(data))
		if err != nil {
			return nil, fmt.Errorf("decoding stream chunk: %w", err)
		}
		var chunk StreamChunk
		if err := dd.BindJSON(&chunk, []byte(data)); err != nil {
			return nil, fmt.Errorf("binding stream chunk: %w", err)
		}
		if rawChoices, ok := decoded["choices"].([]any); ok {
			for i, rawChoice := range rawChoices {
				choice, ok := rawChoice.(map[string]any)
				if !ok || i >= len(chunk.Choices) {
					continue
				}
				_, chunk.Choices[i].DeprecatedFunctionCall = choice["function_call"]
				if rawDelta, ok := choice["delta"].(map[string]any); ok {
					_, chunk.Choices[i].Delta.DeprecatedFunctionCall = rawDelta["function_call"]
				}
			}
		}

		return &chunk, nil
	}
}

func (s *StreamReader) Close() error {
	return s.body.Close()
}
