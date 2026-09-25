package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// RoundAdapter communicates with one model backend for one complete round.
type RoundAdapter interface {
	Round(ctx context.Context, request RoundRequest, emit func(RoundEvent)) (RoundFinal, error)
}

type RoundIntent string

const (
	IntentTools RoundIntent = "tools"
	IntentFinal RoundIntent = "final"
)

type RoundRequest struct {
	Messages     []Message
	Tools        []Tool
	Model        string
	MaxTokens    int
	Intent       RoundIntent
	Iteration    int
	Continuation *Continuation
}

type RoundEvent struct {
	Kind    string
	Content string
	Call    RoundCall
	Usage   *Usage
}

type RoundCall struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

type RoundFinal struct {
	Finish       string
	Content      string
	Calls        []RoundCall
	Continuation *Continuation
}

type RoundIdentity struct {
	Provider      string `dd:"provider"`
	Protocol      string `dd:"protocol"`
	UpstreamModel string `dd:"upstream_model"`
	Service       string `dd:"service"`
	Profile       string `dd:"profile,+omitempty"`
	AccountScope  string `dd:"account_scope,+omitempty"`
}

type RoundOrigin struct {
	Alias           string        `dd:"alias"`
	Identity        RoundIdentity `dd:"identity"`
	RequestedEffort string        `dd:"requested_effort,+omitempty"`
	EffectiveEffort string        `dd:"effective_effort,+omitempty"`
}

type CallBinding struct {
	PaneCallID     string `dd:"pane_call_id"`
	ProviderCallID string `dd:"provider_call_id"`
	ProviderItemID string `dd:"provider_item_id"`
}

type Continuation struct {
	Format   string            `dd:"format"`
	Version  int               `dd:"v"`
	Identity RoundIdentity     `dd:"identity"`
	Items    []json.RawMessage `dd:"items"`
	Bindings []CallBinding     `dd:"bindings,+omitempty"`
}

type RoundError struct {
	Kind   string
	Reason string
	Err    error
}

func (e *RoundError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s: %s", e.Kind, e.Reason)
	}
	return fmt.Sprintf("%s: %v", e.Kind, e.Err)
}

func (e *RoundError) Unwrap() error { return e.Err }

type Dispatch string

const (
	NotDispatched   Dispatch = "not_dispatched"
	ResultReceived  Dispatch = "result_received"
	UnknownDispatch Dispatch = "unknown"
)

// ToolExecution separates dispatch evidence from the diagnostic error.
type ToolExecution struct {
	Dispatch Dispatch
	Content  string
	Duration time.Duration
	IsError  bool
	Err      error
}
