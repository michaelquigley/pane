package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"reflect"
	"time"

	"github.com/michaelquigley/df/dd"
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
	Messages       []Message
	Tools          []Tool
	Model          string
	MaxTokens      int
	Intent         RoundIntent
	Iteration      int
	Continuation   *Continuation
	LocalReasoning map[string]string
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
	Finish         string
	Content        string
	Calls          []RoundCall
	Continuation   *Continuation
	Origin         *RoundOrigin
	LocalReasoning string
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

// MarshalDd keeps opaque provider items as JSON objects in pane events.
func (c Continuation) MarshalDd() (map[string]any, error) {
	identity, err := dd.Unbind(c.Identity)
	if err != nil {
		return nil, err
	}
	items := make([]any, 0, len(c.Items))
	for _, raw := range c.Items {
		item, err := dd.DecodeStrictJSON(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	payload := map[string]any{"format": c.Format, "v": c.Version, "identity": identity, "items": items}
	if len(c.Bindings) > 0 {
		bindings := make([]any, 0, len(c.Bindings))
		for _, binding := range c.Bindings {
			value, err := dd.Unbind(binding)
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, value)
		}
		payload["bindings"] = bindings
	}
	return payload, nil
}

// continuationVersionConverter normalizes the envelope's version number into
// the destination int type across both intake postures: strict intake yields
// json.Number, forgiving intake yields float64. non-finite values,
// fractions, and out-of-range values are rejected rather than truncated.
type continuationVersionConverter struct{}

func (continuationVersionConverter) FromRaw(raw any) (any, error) {
	// a float64 maps into int exactly only within the half-open range
	// [-2^(intBits-1), 2^(intBits-1)), both bounds exactly representable in
	// float64. the range is checked on the float before conversion, because
	// float64(math.MaxInt) rounds up to the upper bound and converting it
	// wraps to a negative integer.
	upper := float64(1 << (bits.UintSize - 1))
	lower := -upper
	switch n := raw.(type) {
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return nil, fmt.Errorf("version %s is not an integer", n)
		}
		if i < math.MinInt || i > math.MaxInt {
			return nil, fmt.Errorf("version %s is out of range", n)
		}
		return int(i), nil
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return nil, fmt.Errorf("version %v is not an integer", n)
		}
		if n < lower || n >= upper {
			return nil, fmt.Errorf("version %v is out of range", n)
		}
		return int(n), nil
	case int:
		return n, nil
	case int64:
		if n < math.MinInt || n > math.MaxInt {
			return nil, fmt.Errorf("version %d is out of range", n)
		}
		return int(n), nil
	}
	return nil, fmt.Errorf("version %T is not a number", raw)
}

func (continuationVersionConverter) ToRaw(value any) (any, error) { return value, nil }

// continuationBindOpts binds the envelope's named fields under strict typed
// rules: the integer version through the converter, explicit nulls as
// absent on the optional fields, and unknown keys captured into the +extra
// fields where they are discarded rather than interpreted.
var continuationBindOpts = &dd.Options{
	Strict: true,
	Converters: map[reflect.Type]dd.Converter{
		reflect.TypeOf(0): continuationVersionConverter{},
	},
}

// continuationIdentityWire binds the envelope's identity subtree with
// private tolerance rules: unknown keys are captured and discarded, and
// explicit nulls bind as absent. recognized fields are copied into the
// shared RoundIdentity type, which keeps its own mappings unchanged.
type continuationIdentityWire struct {
	Provider      string         `dd:",+nullable"`
	Protocol      string         `dd:",+nullable"`
	UpstreamModel string         `dd:",+nullable"`
	Service       string         `dd:",+nullable"`
	Profile       string         `dd:",+nullable"`
	AccountScope  string         `dd:",+nullable"`
	Extra         map[string]any `dd:",+extra"`
}

// callBindingWire binds one pane-call/provider-call/provider-item binding
// with the same private tolerance as continuationIdentityWire.
type callBindingWire struct {
	PaneCallID     string         `dd:",+nullable"`
	ProviderCallID string         `dd:",+nullable"`
	ProviderItemID string         `dd:",+nullable"`
	Extra          map[string]any `dd:",+extra"`
}

// continuationWire is the bind-only view over the envelope's named fields;
// items stay outside it, handled as opaque provider payloads.
type continuationWire struct {
	Format   string                    `dd:",+nullable"`
	Version  int                       `dd:"v"`
	Identity *continuationIdentityWire `dd:"identity,+nullable"`
	Bindings []callBindingWire         `dd:"bindings,+nullable"`
	Extra    map[string]any            `dd:",+extra"`
}

// UnmarshalDd restores the envelope's named fields through dd and its
// opaque provider items separately: each item is a JSON object re-encoded
// as supplied, never interpreted, so unknown keys and exact number lexemes
// available in the supplied tree survive. values already rounded by a
// forgiving intake (float64) are preserved rounded; precision lost before
// binding cannot be recovered here.
func (c *Continuation) UnmarshalDd(data map[string]any) error {
	rest := make(map[string]any, len(data))
	var itemsRaw any
	itemsPresent := false
	for key, value := range data {
		if key == "items" {
			itemsRaw = value
			itemsPresent = true
			continue
		}
		rest[key] = value
	}
	var wire continuationWire
	if err := dd.Bind(&wire, rest, continuationBindOpts); err != nil {
		return err
	}
	c.Format = wire.Format
	c.Version = wire.Version
	if wire.Identity != nil {
		c.Identity = RoundIdentity{
			Provider:      wire.Identity.Provider,
			Protocol:      wire.Identity.Protocol,
			UpstreamModel: wire.Identity.UpstreamModel,
			Service:       wire.Identity.Service,
			Profile:       wire.Identity.Profile,
			AccountScope:  wire.Identity.AccountScope,
		}
	}
	c.Bindings = make([]CallBinding, 0, len(wire.Bindings))
	for _, binding := range wire.Bindings {
		c.Bindings = append(c.Bindings, CallBinding{
			PaneCallID:     binding.PaneCallID,
			ProviderCallID: binding.ProviderCallID,
			ProviderItemID: binding.ProviderItemID,
		})
	}
	if itemsPresent {
		elements, ok := itemsRaw.([]any)
		if !ok {
			return fmt.Errorf("continuation items are not an array")
		}
		items := make([]json.RawMessage, 0, len(elements))
		for index, element := range elements {
			object, ok := element.(map[string]any)
			if !ok {
				return fmt.Errorf("continuation item %d is not an object", index)
			}
			raw, err := json.Marshal(object)
			if err != nil {
				return err
			}
			items = append(items, raw)
		}
		c.Items = items
	}
	return nil
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
