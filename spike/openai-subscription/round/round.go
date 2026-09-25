// Package round holds the provider-neutral round contract proposed by the
// spike: portable messages, origin/continuation metadata, normalized stream
// events, and the finalized round an adapter returns. adapters communicate
// with models; they never execute tools.
package round

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Message is the portable, cross-provider record of one conversation entry.
// Origin, Continuation, and Local are assistant-only.
type Message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`

	// Origin records the resolved connection that produced an assistant
	// message. nil means legacy history without provenance.
	Origin *Origin `json:"origin,omitempty"`

	// Continuation is the versioned, opaque replay envelope for this
	// assistant round. the browser preserves it without interpreting it.
	Continuation *Envelope `json:"continuation,omitempty"`
}

// ToolCall is a portable tool call. ID is pane-owned and provider-neutral;
// provider call ids live in the continuation envelope's bindings.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is a callable tool offered to the model (already model-safe named).
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Origin is assistant-message provenance. Identity is the replay-relevant
// part; Alias and Effort are provenance only and never gate replay.
type Origin struct {
	Alias    string   `json:"alias"`
	Effort   string   `json:"effort,omitempty"`
	Identity Identity `json:"identity"`
}

// Identity is the resolved connection a continuation is bound to. it never
// contains credentials or credential fingerprints.
type Identity struct {
	Provider      string `json:"provider"`       // "openai-codex" | "openai-chat-completions"
	Protocol      string `json:"protocol"`       // "responses" | "chat-completions"
	UpstreamModel string `json:"upstream_model"` // the model id sent upstream
	Service       string `json:"service"`        // provider service identity or endpoint origin
	Profile       string `json:"profile,omitempty"`
	AccountScope  string `json:"account_scope,omitempty"` // non-secret, derived account binding
}

// Equal reports exact identity equality; compatibility is deliberately
// conservative.
func (i Identity) Equal(o Identity) bool { return i == o }

// EnvelopeVersion is the only continuation format this spike emits/accepts.
const EnvelopeVersion = 1

// Envelope is the durable per-assistant-round continuation record.
type Envelope struct {
	Version  int      `json:"v"`
	Format   string   `json:"format"` // e.g. "codex-responses-items"
	Identity Identity `json:"identity"`

	// Items are the provider's ordered output items for the round, verbatim
	// except for fields the adapter strips (never credentials).
	Items []json.RawMessage `json:"items"`

	// Bindings tie each portable tool call to the provider's call ids.
	Bindings []Binding `json:"bindings,omitempty"`
}

// Binding maps a pane tool-call id to the provider's call and item ids.
type Binding struct {
	CallID         string `json:"call_id"`          // pane id
	ProviderCallID string `json:"provider_call_id"` // provider call_id
	ProviderItemID string `json:"provider_item_id,omitempty"`
}

// Intent is the round's purpose.
type Intent int

const (
	IntentNormal Intent = iota
	// IntentForcedFinal asks for a final answer with tools withheld.
	IntentForcedFinal
)

// ForcedFinalInstruction is the recovery instruction. its placement is the
// adapter's (profile's) concern.
const ForcedFinalInstruction = "tool calls are disabled because the same tool call failed repeatedly. provide a final answer without calling tools."

// Request is one upstream generation round.
type Request struct {
	History []Message
	Tools   []Tool
	Intent  Intent

	// Local carries request-local data keyed by History index -- the active
	// qwen tool loop's reasoning. it is never persisted.
	Local map[int]Local

	// CacheKey is an optional non-secret prompt-cache affinity key.
	CacheKey string
}

// Local is request-local, never-persisted round data.
type Local struct {
	Reasoning string
}

// EventKind enumerates normalized progressive events. previews are display
// only; nothing here is executable.
type EventKind string

const (
	EventText           EventKind = "text_delta"
	EventThinking       EventKind = "thinking_delta"
	EventToolCallStart  EventKind = "tool_call_start"
	EventToolCallArgs   EventKind = "tool_call_args"
	EventToolCallFrozen EventKind = "tool_call_done" // provider says arguments are complete; still not executable
)

// Event is a normalized progressive display event.
type Event struct {
	Kind  EventKind `json:"kind"`
	Index int       `json:"index,omitempty"` // tool-call ordinal within the round
	Name  string    `json:"name,omitempty"`
	Delta string    `json:"delta,omitempty"`
}

// Finish is the normalized completion status of a round.
type Finish string

const (
	FinishStop      Finish = "stop"
	FinishToolCalls Finish = "tool_calls"
	FinishLength    Finish = "length"
	FinishFiltered  Finish = "content_filter"
)

// Usage is normalized token usage. zero values mean unreported.
type Usage struct {
	InputTokens     int `json:"input_tokens"`
	CachedTokens    int `json:"cached_tokens,omitempty"`
	OutputTokens    int `json:"output_tokens"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	TotalTokens     int `json:"total_tokens"`
}

// Final is a finalized assistant round. it exists only when the provider's
// terminal event was observed.
type Final struct {
	Assistant Message `json:"assistant"`
	Finish    Finish  `json:"finish"`
	RawFinish string  `json:"raw_finish"`
	Usage     *Usage  `json:"usage,omitempty"`
	Local     *Local  `json:"-"`
}

// Executable reports whether the round's tool calls may be dispatched: a
// terminal tool-requesting round whose calls all validated.
func (f *Final) Executable() bool {
	return f != nil && f.Finish == FinishToolCalls && len(f.Assistant.ToolCalls) > 0
}

// ErrorKind classifies round failures. none of them triggers an automatic
// retry in the harness.
type ErrorKind string

const (
	ErrTransport      ErrorKind = "transport"       // connection failed or dropped
	ErrTruncated      ErrorKind = "truncated"       // stream ended without terminal event
	ErrProtocol       ErrorKind = "protocol"        // malformed provider data
	ErrCancelled      ErrorKind = "cancelled"       // caller cancelled
	ErrAuth           ErrorKind = "auth"            // missing/invalid/revoked credentials
	ErrAllowance      ErrorKind = "allowance"       // subscription usage exhausted
	ErrRateLimited    ErrorKind = "rate_limited"    // transient throttling
	ErrModelAccess    ErrorKind = "model_access"    // account/model not permitted
	ErrInvalidRequest ErrorKind = "invalid_request" // provider rejected the request
	ErrUpstream       ErrorKind = "upstream"        // provider 5xx or failed response
	ErrIncomplete     ErrorKind = "incomplete"      // terminal but unusable (length, filter, invalid calls)
	ErrBudget         ErrorKind = "budget"          // local request ceiling reached
	ErrDestination    ErrorKind = "destination"     // credential destination violation
)

// Partial is what was observed before a round failed. tool calls here were
// never executable.
type Partial struct {
	Text      string     `json:"text,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Error is a classified round failure.
type Error struct {
	Kind    ErrorKind
	Status  int
	Message string
	Partial Partial
	Err     error
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("%s (status %d): %s", e.Kind, e.Status, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// KindOf returns the classified kind of err, or "" if unclassified.
func KindOf(err error) ErrorKind {
	var re *Error
	if errors.As(err, &re) {
		return re.Kind
	}
	return ""
}

// Adapter performs exactly one upstream generation round per call.
type Adapter interface {
	Identity() Identity
	Alias() string
	Round(ctx context.Context, req Request, emit func(Event)) (*Final, error)
}

// Str is a convenience for *string content.
func Str(s string) *string { return &s }
