package llm

import (
	"encoding/json"

	"github.com/michaelquigley/df/dd"
)

// ChatRequest is an OpenAI-compatible chat completion request.
type ChatRequest struct {
	Model         string         `dd:"model"`
	Messages      []Message      `dd:"messages"`
	Tools         []Tool         `dd:"tools,+omitempty"`
	Stream        bool           `dd:"stream"`
	StreamOptions *StreamOptions `dd:"stream_options,+omitempty"`
	MaxTokens     int            `dd:"max_tokens,+omitempty"`
}

type StreamOptions struct {
	IncludeUsage bool `dd:"include_usage"`
}

// Message is an OpenAI-compatible chat message.
type Message struct {
	Role       string     `dd:"role"`
	Content    *string    `dd:",+nullable"`
	ToolCalls  []ToolCall `dd:"tool_calls,+omitempty"`
	ToolCallID string     `dd:"tool_call_id,+omitempty"`
}

// Message.MarshalDd preserves the chat message's explicit null content on both
// upstream requests and pane round events.
func (m Message) MarshalDd() (map[string]any, error) {
	payload, err := dd.Unbind(struct {
		Role       string
		ToolCalls  []ToolCall `dd:",+omitempty"`
		ToolCallID string     `dd:",+omitempty"`
	}{Role: m.Role, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID})
	if err != nil {
		return nil, err
	}
	if m.Content == nil {
		payload["content"] = nil
	} else {
		payload["content"] = *m.Content
	}
	return payload, nil
}

// StringContent is a convenience for creating a Message with string content.
func StringContent(s string) *string {
	return &s
}

// Tool is an OpenAI-compatible tool definition.
type Tool struct {
	Type     string       `dd:"type"`
	Function *FunctionDef `dd:"function"`
}

// FunctionDef is an OpenAI-compatible function definition.
type FunctionDef struct {
	Name        string          `dd:"name"`
	Description string          `dd:"description"`
	Parameters  json.RawMessage `dd:"-"`
}

// ToolCall is an OpenAI-compatible tool call (in both request and streaming response).
type ToolCall struct {
	ID       string           `dd:"id,+omitempty"`
	Type     string           `dd:"type,+omitempty"`
	Index    *int             `dd:",+nullable,+omitempty"`
	Function ToolCallFunction `dd:"function"`
}

// ToolCallFunction holds the function name and arguments of a tool call.
type ToolCallFunction struct {
	Name      string `dd:"name,+omitempty"`
	Arguments string `dd:"arguments,+omitempty"`
}

// StreamChunk is a single chunk from an OpenAI streaming response.
type StreamChunk struct {
	ID      string   `dd:"id"`
	Choices []Choice `dd:"choices"`
	Usage   *Usage   `dd:",+nullable,+omitempty"`
}

type Usage struct {
	PromptTokens     int `dd:"prompt_tokens"`
	CompletionTokens int `dd:"completion_tokens"`
	TotalTokens      int `dd:"total_tokens"`
}

// Choice is a single choice in a streaming chunk.
type Choice struct {
	Index                  int     `dd:"index"`
	Delta                  Delta   `dd:"delta"`
	FinishReason           *string `dd:",+nullable"`
	DeprecatedFunctionCall bool    `dd:"-"`
}

// Delta is the incremental content in a streaming chunk.
type Delta struct {
	Content                *string    `dd:",+nullable,+omitempty"`
	ToolCalls              []ToolCall `dd:"tool_calls,+omitempty"`
	Reasoning              *string    `dd:",+nullable,+omitempty"`
	ReasoningContent       *string    `dd:",+nullable,+omitempty"`
	DeprecatedFunctionCall bool       `dd:"-"`
}

// ModelsResponse is the response from GET /v1/models.
type ModelsResponse struct {
	Object string  `dd:"object"`
	Data   []Model `dd:"data"`
}

// Model is a single model entry.
type Model struct {
	ID      string `dd:"id"`
	Object  string `dd:"object"`
	OwnedBy string `dd:"owned_by"`
}
