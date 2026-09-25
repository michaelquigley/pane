package llm

import (
	"strings"
	"testing"

	"github.com/michaelquigley/df/dd"
)

func TestDeltaUnmarshalToleratesReasoningSpellings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		wire string
		want *string
	}{
		{
			name: "reasoning alone",
			wire: `{"reasoning":"checking the layout"}`,
			want: StringContent("checking the layout"),
		},
		{
			name: "reasoning_content alone",
			wire: `{"reasoning_content":"checking the layout"}`,
			want: StringContent("checking the layout"),
		},
		{
			name: "both spellings prefer reasoning",
			wire: `{"reasoning":"a","reasoning_content":"b"}`,
			want: StringContent("a"),
		},
		{
			name: "neither spelling",
			wire: `{"content":"hello"}`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var delta Delta
			if err := dd.BindJSON(&delta, []byte(tt.wire)); err != nil {
				t.Fatalf("unmarshaling delta: %v", err)
			}
			reasoning := delta.Reasoning
			if reasoning == nil {
				reasoning = delta.ReasoningContent
			}
			if (reasoning == nil) != (tt.want == nil) {
				t.Fatalf("expected reasoning %#v, got %#v", tt.want, reasoning)
			}
			if reasoning != nil && tt.want != nil && *reasoning != *tt.want {
				t.Fatalf("expected reasoning %q, got %q", *tt.want, *reasoning)
			}
		})
	}
}

func TestDeltaUnmarshalParsesContentAndToolCallsAlongsideReasoning(t *testing.T) {
	t.Parallel()

	wire := `{"content":"hello","reasoning":"thinking","tool_calls":[{"index":0,"function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]}`

	var delta Delta
	if err := dd.BindJSON(&delta, []byte(wire)); err != nil {
		t.Fatalf("unmarshaling delta: %v", err)
	}

	if delta.Content == nil || *delta.Content != "hello" {
		t.Fatalf("unexpected content: %#v", delta.Content)
	}
	if delta.Reasoning == nil || *delta.Reasoning != "thinking" {
		t.Fatalf("unexpected reasoning: %#v", delta.Reasoning)
	}
	if len(delta.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(delta.ToolCalls))
	}
	if delta.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected tool call name %q", delta.ToolCalls[0].Function.Name)
	}
	if delta.ToolCalls[0].Function.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected tool call arguments %q", delta.ToolCalls[0].Function.Arguments)
	}
}

func TestDeltaRoundTripPreservesReasoning(t *testing.T) {
	t.Parallel()

	delta := Delta{
		Content:   StringContent("hello"),
		Reasoning: StringContent("thinking"),
	}

	data, err := dd.UnbindJSON(delta)
	if err != nil {
		t.Fatalf("marshaling delta: %v", err)
	}
	if !strings.Contains(string(data), `"reasoning": "thinking"`) {
		t.Fatalf("expected marshaled delta to use the reasoning spelling, got %s", data)
	}

	var got Delta
	if err := dd.BindJSON(&got, data); err != nil {
		t.Fatalf("unmarshaling delta: %v", err)
	}

	if got.Content == nil || *got.Content != "hello" {
		t.Fatalf("unexpected content: %#v", got.Content)
	}
	if got.Reasoning == nil || *got.Reasoning != "thinking" {
		t.Fatalf("unexpected reasoning: %#v", got.Reasoning)
	}
}
