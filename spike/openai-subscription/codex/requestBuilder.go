package codex

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

// EnvelopeFormat names the continuation format this adapter writes.
const EnvelopeFormat = "codex-responses-items"

// effortTables are source-established from pi-ai 0.87.1's openai-codex
// catalog (thinkingLevelMap), not route-verified. 'minimal' is rejected
// rather than translated to 'low' as pi does. 'none' is listed only where pi
// would send effort 'none'; astra maps pi's 'off' to null (no field), so it
// cannot be disabled by request.
var effortTables = map[string][]string{
	"gpt-5.6-sol": {"none", "low", "medium", "high", "xhigh", "max"},
	"gpt-6-astra": {"low", "medium", "high", "xhigh", "max"},
}

// ValidateEffort fails closed for unknown models or unsupported values.
// empty effort means omit the field and keep the upstream default.
func ValidateEffort(model, effort string) error {
	if effort == "" {
		return nil
	}
	values, ok := effortTables[model]
	if !ok {
		return fmt.Errorf("no verified reasoning_effort table for model '%s'", model)
	}
	for _, v := range values {
		if v == effort {
			return nil
		}
	}
	return fmt.Errorf("reasoning_effort '%s' is not supported for model '%s' (supported: %s)", effort, model, strings.Join(values, ", "))
}

type requestBody struct {
	Model             string            `json:"model"`
	Store             bool              `json:"store"`
	Stream            bool              `json:"stream"`
	Instructions      string            `json:"instructions"`
	Input             []json.RawMessage `json:"input"`
	Tools             []toolDef         `json:"tools,omitempty"`
	ToolChoice        string            `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool             `json:"parallel_tool_calls,omitempty"`
	Reasoning         *reasoningParams  `json:"reasoning,omitempty"`
	Include           []string          `json:"include"`
	PromptCacheKey    string            `json:"prompt_cache_key,omitempty"`
	MaxOutputTokens   int               `json:"max_output_tokens,omitempty"`
}

type reasoningParams struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type toolDef struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

// input item shapes the adapter synthesizes from portable history.
type inputText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type roleMessage struct {
	Role    string      `json:"role"`
	Content []inputText `json:"content"`
}

type assistantMessageItem struct {
	Type    string       `json:"type"`
	Role    string       `json:"role"`
	ID      string       `json:"id"`
	Status  string       `json:"status"`
	Content []outputText `json:"content"`
}

type outputText struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}

type functionCallItem struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type functionCallOutputItem struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

// Decision records why each assistant message's continuation was or was not
// replayed. it is diagnostic output for tests and reports.
type Decision struct {
	Index    int    `json:"index"`
	Replayed bool   `json:"replayed"`
	Reason   string `json:"reason"`
}

var providerIDUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// portableCallID derives a provider-acceptable call id from a pane id.
func portableCallID(id string) string {
	s := providerIDUnsafe.ReplaceAllString(id, "_")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// buildRequest converts portable history into a responses request. it never
// mutates the history. the returned decisions explain replay filtering.
func (a *Adapter) buildRequest(req round.Request, identity round.Identity) (*requestBody, []Decision, error) {
	body := &requestBody{
		Model:   a.cfg.UpstreamModel,
		Store:   false,
		Stream:  true,
		Include: []string{"reasoning.encrypted_content"},
	}
	if a.cfg.Effort != "" {
		body.Reasoning = &reasoningParams{Effort: a.cfg.Effort, Summary: "auto"}
	}
	if a.cfg.MaxOutputTokens > 0 {
		body.MaxOutputTokens = a.cfg.MaxOutputTokens
	}
	body.PromptCacheKey = req.CacheKey

	var instructions []string
	var decisions []Decision
	// callIDs maps pane tool-call ids to the provider call id used on the
	// wire, so outputs pair with whichever call representation was sent.
	callIDs := make(map[string]string)
	pendingOutputs := make(map[string]bool)

	for i, m := range req.History {
		switch m.Role {
		case "system":
			text := deref(m.Content)
			if i == 0 {
				instructions = append(instructions, text)
			} else if text != "" {
				body.Input = append(body.Input, mustJSON(roleMessage{Role: "developer", Content: []inputText{{Type: "input_text", Text: text}}}))
			}
		case "user":
			if len(pendingOutputs) > 0 {
				return nil, decisions, orphanError(pendingOutputs)
			}
			body.Input = append(body.Input, mustJSON(roleMessage{Role: "user", Content: []inputText{{Type: "input_text", Text: deref(m.Content)}}}))
		case "assistant":
			if len(pendingOutputs) > 0 {
				return nil, decisions, orphanError(pendingOutputs)
			}
			items, d := a.assistantItems(i, m, identity, callIDs)
			decisions = append(decisions, d)
			body.Input = append(body.Input, items...)
			for _, tc := range m.ToolCalls {
				pendingOutputs[tc.ID] = true
			}
		case "tool":
			wireID, ok := callIDs[m.ToolCallID]
			if !ok || !pendingOutputs[m.ToolCallID] {
				return nil, decisions, &round.Error{Kind: round.ErrInvalidRequest, Message: fmt.Sprintf("tool result '%s' has no preceding call", m.ToolCallID)}
			}
			delete(pendingOutputs, m.ToolCallID)
			body.Input = append(body.Input, mustJSON(functionCallOutputItem{Type: "function_call_output", CallID: wireID, Output: deref(m.Content)}))
		default:
			return nil, decisions, &round.Error{Kind: round.ErrInvalidRequest, Message: fmt.Sprintf("unsupported role '%s'", m.Role)}
		}
	}
	if len(pendingOutputs) > 0 {
		return nil, decisions, orphanError(pendingOutputs)
	}

	if req.Intent == round.IntentForcedFinal {
		instructions = append(instructions, round.ForcedFinalInstruction)
	} else if len(req.Tools) > 0 {
		for _, t := range req.Tools {
			body.Tools = append(body.Tools, toolDef{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.Parameters, Strict: false})
		}
		body.ToolChoice = "auto"
		parallel := true
		body.ParallelToolCalls = &parallel
	}
	body.Instructions = strings.TrimSpace(strings.Join(nonEmpty(instructions), "\n\n"))
	return body, decisions, nil
}

func orphanError(pending map[string]bool) error {
	return &round.Error{Kind: round.ErrInvalidRequest, Message: fmt.Sprintf("%d tool call(s) lack results; history must be reconciled before sending", len(pending))}
}

// assistantItems replays a compatible envelope verbatim, else synthesizes
// portable items. foreign reasoning is dropped, never converted to text.
func (a *Adapter) assistantItems(index int, m round.Message, identity round.Identity, callIDs map[string]string) ([]json.RawMessage, Decision) {
	if items, bindings, reason := validateEnvelope(m, identity); reason == "" {
		for _, b := range bindings {
			callIDs[b.CallID] = b.ProviderCallID
		}
		return items, Decision{Index: index, Replayed: true, Reason: "compatible"}
	} else {
		var out []json.RawMessage
		if text := deref(m.Content); text != "" {
			out = append(out, mustJSON(assistantMessageItem{
				Type: "message", Role: "assistant", ID: fmt.Sprintf("msg_pane_%d", index), Status: "completed",
				Content: []outputText{{Type: "output_text", Text: text, Annotations: []any{}}},
			}))
		}
		for _, tc := range m.ToolCalls {
			wire := portableCallID(tc.ID)
			callIDs[tc.ID] = wire
			out = append(out, mustJSON(functionCallItem{Type: "function_call", CallID: wire, Name: tc.Name, Arguments: tc.Arguments}))
		}
		return out, Decision{Index: index, Replayed: false, Reason: reason}
	}
}

// MaxEnvelopeBytes bounds a replayable envelope. larger envelopes stay
// stored but are excluded from requests. the value is a spike placeholder.
const MaxEnvelopeBytes = 512 * 1024

// validateEnvelope applies the accepted fail-closed checks. it returns the
// replay items and bindings, or a non-empty reason for exclusion.
func validateEnvelope(m round.Message, identity round.Identity) ([]json.RawMessage, []round.Binding, string) {
	env := m.Continuation
	switch {
	case env == nil:
		if m.Origin == nil {
			return nil, nil, "no provenance (legacy history)"
		}
		return nil, nil, "no continuation"
	case env.Version != round.EnvelopeVersion:
		return nil, nil, fmt.Sprintf("unsupported envelope version %d", env.Version)
	case env.Format != EnvelopeFormat:
		return nil, nil, fmt.Sprintf("foreign format '%s'", env.Format)
	case !env.Identity.Equal(identity):
		return nil, nil, "identity mismatch"
	case m.Origin == nil || !m.Origin.Identity.Equal(env.Identity):
		return nil, nil, "origin does not match envelope"
	}
	size := 0
	for _, it := range env.Items {
		size += len(it)
	}
	if size > MaxEnvelopeBytes {
		return nil, nil, "envelope exceeds size limit"
	}

	// structural checks: allowed item types, reasoning followed by an item,
	// text and calls matching the portable record, one binding per call.
	var text strings.Builder
	type seenCall struct{ name, args, itemID string }
	calls := make(map[string]seenCall)
	for i, raw := range env.Items {
		var probe struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, nil, "malformed item"
		}
		switch probe.Type {
		case "reasoning":
			if i == len(env.Items)-1 {
				return nil, nil, "reasoning item without following item"
			}
		case "message":
			for _, c := range probe.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				} else if c.Type == "refusal" {
					text.WriteString(c.Refusal)
				}
			}
		case "function_call":
			if probe.CallID == "" {
				return nil, nil, "function_call without call_id"
			}
			if _, dup := calls[probe.CallID]; dup {
				return nil, nil, "duplicate provider call id"
			}
			calls[probe.CallID] = seenCall{name: probe.Name, args: probe.Arguments, itemID: probe.ID}
		default:
			return nil, nil, fmt.Sprintf("unsupported item type '%s'", probe.Type)
		}
	}
	if text.String() != deref(m.Content) {
		return nil, nil, "envelope text does not match portable content"
	}
	if len(env.Bindings) != len(m.ToolCalls) || len(calls) != len(m.ToolCalls) {
		return nil, nil, "tool call count mismatch"
	}
	byPane := make(map[string]round.Binding, len(env.Bindings))
	for _, b := range env.Bindings {
		byPane[b.CallID] = b
	}
	for _, tc := range m.ToolCalls {
		b, ok := byPane[tc.ID]
		if !ok {
			return nil, nil, "unbound tool call"
		}
		c, ok := calls[b.ProviderCallID]
		if !ok || c.name != tc.Name || c.args != tc.Arguments || c.itemID != b.ProviderItemID {
			return nil, nil, "binding does not match provider item"
		}
	}
	return env.Items, env.Bindings, ""
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nonEmpty(in []string) []string {
	out := in[:0:0]
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
