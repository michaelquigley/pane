package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/llm"
)

// inputTextPart is one text part of a constructed user or developer item.
type inputTextPart struct {
	Type string
	Text string
}

// inputMessageItem is a constructed user or developer message item.
type inputMessageItem struct {
	Role    string
	Content []inputTextPart
}

// assistantTextPart is one content part of a constructed assistant message item.
type assistantTextPart struct {
	Type        string
	Text        string
	Annotations []any
}

// assistantMessageItem is a constructed assistant message item for portable
// fallback.
type assistantMessageItem struct {
	Type    string
	Role    string
	Status  string
	Content []assistantTextPart
}

// functionCallItem is a constructed function call item for portable fallback.
type functionCallItem struct {
	Type      string
	CallID    string
	Name      string
	Arguments string
}

// functionCallOutputItem is a constructed tool result item.
type functionCallOutputItem struct {
	Type   string
	CallID string
	Output string
}

// itemPayload unbinds one constructed request item to the wire tree the
// final request encoding serializes.
func itemPayload(value any) (map[string]any, error) {
	return dd.Unbind(value)
}

func portableCallID(id string) string {
	hash := sha256.Sum256([]byte("pane-portable-call-v1:" + id))
	return "pane_" + hex.EncodeToString(hash[:24])
}

func objectArguments(raw string) bool {
	value, err := dd.DecodeStrictJSON([]byte(raw))
	if err != nil {
		return false
	}
	_, ok := any(value).(map[string]any)
	return ok
}

func (a *Adapter) buildRequest(req llm.RoundRequest) ([]byte, error) {
	if req.MaxTokens > 0 {
		return nil, errors.New("max_tokens is unsupported for subscription generation")
	}
	body := requestBody{Model: a.model, Stream: true, Include: []string{"reasoning.encrypted_content"}}
	if a.effort != "" {
		body.Reasoning = &reasoning{Effort: a.effort, Summary: "auto"}
	}
	callIDs := make(map[string]string)
	seenCalls := make(map[string]bool)
	var pending []string
	var instructions []string
	for i, message := range req.Messages {
		content := ""
		if message.Content != nil {
			content = *message.Content
		}
		switch message.Role {
		case "system":
			if len(pending) > 0 {
				return nil, errors.New("system message interrupts tool results")
			}
			if i == 0 || req.Intent == llm.IntentFinal && i == len(req.Messages)-1 {
				instructions = append(instructions, content)
			} else if content != "" {
				item, err := itemPayload(inputMessageItem{Role: "developer", Content: []inputTextPart{{Type: "input_text", Text: content}}})
				if err != nil {
					return nil, err
				}
				body.Input = append(body.Input, item)
			}
		case "user":
			if len(pending) > 0 {
				return nil, errors.New("tool call lacks result before user message")
			}
			item, err := itemPayload(inputMessageItem{Role: "user", Content: []inputTextPart{{Type: "input_text", Text: content}}})
			if err != nil {
				return nil, err
			}
			body.Input = append(body.Input, item)
		case "assistant":
			if len(pending) > 0 {
				return nil, errors.New("tool call lacks result before assistant message")
			}
			// pane call ids are unique across the whole request, checked before
			// the replay path is chosen so compatible and portable history are
			// held to the same rule.
			for _, call := range message.ToolCalls {
				if seenCalls[call.ID] {
					return nil, errors.New("duplicate tool call id")
				}
				seenCalls[call.ID] = true
			}
			items, bindings, compatible := validatedEnvelope(message, a.identity)
			if compatible {
				// compatible items are opaque provider payloads: decode each
				// through the same strict path as every other wire value, so the
				// final encoding carries their exact string values, number
				// lexemes, and unmodeled fields without reinterpreting them.
				for _, raw := range items {
					tree, err := dd.DecodeStrictJSON(raw)
					if err != nil {
						return nil, err
					}
					body.Input = append(body.Input, tree)
				}
				for _, binding := range bindings {
					callIDs[binding.PaneCallID] = binding.ProviderCallID
				}
			} else {
				if content != "" {
					item, err := itemPayload(assistantMessageItem{Type: "message", Role: "assistant", Status: "completed", Content: []assistantTextPart{{Type: "output_text", Text: content, Annotations: []any{}}}})
					if err != nil {
						return nil, err
					}
					body.Input = append(body.Input, item)
				}
				for _, call := range message.ToolCalls {
					if call.ID == "" || call.Function.Name == "" || !objectArguments(call.Function.Arguments) {
						return nil, errors.New("invalid portable tool call")
					}
					callIDs[call.ID] = portableCallID(call.ID)
					item, err := itemPayload(functionCallItem{Type: "function_call", CallID: callIDs[call.ID], Name: call.Function.Name, Arguments: call.Function.Arguments})
					if err != nil {
						return nil, err
					}
					body.Input = append(body.Input, item)
				}
			}
			for _, call := range message.ToolCalls {
				pending = append(pending, call.ID)
			}
		case "tool":
			if len(pending) == 0 || message.ToolCallID != pending[0] {
				return nil, errors.New("tool result is missing, duplicated, or out of order")
			}
			wireID := callIDs[message.ToolCallID]
			if wireID == "" {
				return nil, errors.New("tool result has no provider call binding")
			}
			pending = pending[1:]
			item, err := itemPayload(functionCallOutputItem{Type: "function_call_output", CallID: wireID, Output: content})
			if err != nil {
				return nil, err
			}
			body.Input = append(body.Input, item)
		default:
			return nil, fmt.Errorf("unsupported message role '%s'", message.Role)
		}
	}
	if len(pending) > 0 {
		return nil, errors.New("tool call lacks result")
	}
	body.Instructions = strings.TrimSpace(strings.Join(instructions, "\n\n"))
	if req.Intent != llm.IntentFinal && len(req.Tools) > 0 {
		for _, tool := range req.Tools {
			if tool.Function == nil || tool.Type != "function" || tool.Function.Name == "" {
				return nil, errors.New("invalid tool definition")
			}
			parameters, err := dd.DecodeStrictJSON(tool.Function.Parameters)
			if err != nil {
				return nil, errors.New("invalid tool definition")
			}
			body.Tools = append(body.Tools, toolDef{Type: "function", Name: tool.Function.Name, Description: tool.Function.Description, Parameters: parameters, Strict: false})
		}
		body.ToolChoice = "auto"
		parallel := true
		body.ParallelToolCalls = &parallel
	}
	payload, err := dd.Unbind(body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

// outputItem is the typed view over an opaque provider output item. the
// surrounding bytes stay raw in continuation data; only these named fields
// are interpreted for validation. +nullable fields bind explicit provider
// nulls as absent, and +extra absorbs fields pane does not model.
type outputItem struct {
	Type             string         `dd:",+nullable"`
	ID               string         `dd:",+nullable"`
	Status           string         `dd:",+nullable"`
	Role             string         `dd:",+nullable"`
	CallID           string         `dd:",+nullable"`
	Name             string         `dd:",+nullable"`
	Arguments        string         `dd:",+nullable"`
	Content          []contentPart  `dd:",+nullable"`
	EncryptedContent string         `dd:",+nullable"`
	Extra            map[string]any `dd:",+extra"`
}

type contentPart struct {
	Type    string         `dd:",+nullable"`
	Text    string         `dd:",+nullable"`
	Refusal string         `dd:",+nullable"`
	Extra   map[string]any `dd:",+extra"`
}

func decodeItem(raw json.RawMessage) (outputItem, error) {
	var item outputItem
	tree, err := dd.DecodeStrictJSON(raw)
	if err != nil {
		return item, err
	}
	if err := dd.Bind(&item, tree, wireBindOpts); err != nil {
		return item, err
	}
	return item, nil
}

func sameIdentity(a, b llm.RoundIdentity) bool {
	return a.Provider == b.Provider && a.Protocol == b.Protocol && a.UpstreamModel == b.UpstreamModel && a.Service == b.Service && a.AccountScope == b.AccountScope && a.Profile == b.Profile
}

func validatedEnvelope(message llm.Message, identity llm.RoundIdentity) ([]json.RawMessage, []llm.CallBinding, bool) {
	env := message.Continuation
	if env == nil || env.Format != continuationFormat || env.Version != 1 || message.Origin == nil || !sameIdentity(env.Identity, identity) || !sameIdentity(message.Origin.Identity, identity) {
		return nil, nil, false
	}
	var size int
	var text strings.Builder
	// calls holds function call items in provider order; the k-th must agree
	// with Bindings[k] and ToolCalls[k], the order capture records them in.
	var calls []outputItem
	seenCalls := make(map[string]bool)
	seenItems := make(map[string]bool)
	for i, raw := range env.Items {
		size += len(raw)
		if size > 32*1024*1024 {
			return nil, nil, false
		}
		item, err := decodeItem(raw)
		if err != nil || item.ID == "" || seenItems[item.ID] || item.Status != "" && item.Status != "completed" {
			return nil, nil, false
		}
		seenItems[item.ID] = true
		switch item.Type {
		case "reasoning":
			// a trailing reasoning item or run is rejected, matching capture.
			// the supplied sequence is otherwise preserved as is: this does
			// not prove that the following item is the provider's original
			// companion, which the portable record cannot establish.
			if i == len(env.Items)-1 {
				return nil, nil, false
			}
		case "message":
			if item.Role != "assistant" {
				return nil, nil, false
			}
			for _, c := range item.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				} else if c.Type == "refusal" {
					text.WriteString(c.Refusal)
				} else {
					return nil, nil, false
				}
			}
		case "function_call":
			if item.CallID == "" || item.Name == "" || !objectArguments(item.Arguments) {
				return nil, nil, false
			}
			if seenCalls[item.CallID] {
				return nil, nil, false
			}
			seenCalls[item.CallID] = true
			calls = append(calls, item)
		default:
			return nil, nil, false
		}
	}
	content := ""
	if message.Content != nil {
		content = *message.Content
	}
	if text.String() != content || len(env.Bindings) != len(message.ToolCalls) || len(calls) != len(message.ToolCalls) {
		return nil, nil, false
	}
	seenPane := make(map[string]bool)
	for i, binding := range env.Bindings {
		call, item := message.ToolCalls[i], calls[i]
		if binding.PaneCallID != call.ID || seenPane[binding.PaneCallID] || binding.ProviderCallID != item.CallID || binding.ProviderItemID != item.ID || item.Name != call.Function.Name || item.Arguments != call.Function.Arguments {
			return nil, nil, false
		}
		seenPane[binding.PaneCallID] = true
	}
	return env.Items, env.Bindings, true
}
