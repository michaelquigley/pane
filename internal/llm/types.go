package llm

import "encoding/json"

// ChatRequest is an OpenAI-compatible chat completion request.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Stream   bool      `json:"stream"`
}

// Message is an OpenAI-compatible chat message.
type Message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// StringContent is a convenience for creating a Message with string content.
func StringContent(s string) *string {
	return &s
}

// Tool is an OpenAI-compatible tool definition.
type Tool struct {
	Type     string       `json:"type"`
	Function *FunctionDef `json:"function"`
}

// FunctionDef is an OpenAI-compatible function definition.
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall is an OpenAI-compatible tool call (in both request and streaming response).
type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Index    *int             `json:"index,omitempty"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the function name and arguments of a tool call.
type ToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// StreamChunk is a single chunk from an OpenAI streaming response.
type StreamChunk struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
}

// Choice is a single choice in a streaming chunk.
type Choice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

// Delta is the incremental content in a streaming chunk.
type Delta struct {
	Content   *string    `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ModelsResponse is the response from GET /v1/models.
type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model is a single model entry.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}
