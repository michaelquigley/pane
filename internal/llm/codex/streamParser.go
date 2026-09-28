package codex

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/llm"
)

const maxRoundBytes = 32 * 1024 * 1024

var roundSequence atomic.Uint64

// callNamespace is random per process. roundSequence restarts at zero with
// every process, so without it a new call id could repeat one minted by an
// earlier process and already stored in a conversation.
var callNamespace = newCallNamespace()

func newCallNamespace() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails; it crashes the process instead.
	return hex.EncodeToString(b[:])
}

// streamEvent binds one decoded provider SSE event. item and output are
// opaque provider payloads: they are excluded from binding (dd:"-") and
// filled from the exact byte spans opaqueSpans cut from the event line, so
// escaped strings, key order, and number lexemes survive verbatim. the
// +extra field absorbs provider fields pane does not model.
type streamEvent struct {
	Type        string            `dd:",+nullable"`
	OutputIndex int               `dd:",+nullable"`
	Delta       string            `dd:",+nullable"`
	Arguments   string            `dd:",+nullable"`
	Item        json.RawMessage   `dd:"-"`
	Response    *terminalResponse `dd:",+nullable"`
	Code        string            `dd:",+nullable"`
	Error       *providerError    `dd:",+nullable"`
	Extra       map[string]any    `dd:",+extra"`
}
type providerError struct {
	Code    string         `dd:",+nullable"`
	Message string         `dd:",+nullable"`
	Extra   map[string]any `dd:",+extra"`
}
type terminalResponse struct {
	Status            string             `dd:",+nullable"`
	Output            []json.RawMessage  `dd:"-"`
	Error             *providerError     `dd:",+nullable"`
	IncompleteDetails *incompleteDetails `dd:",+nullable"`
	Usage             *terminalUsage     `dd:",+nullable"`
	Extra             map[string]any     `dd:",+extra"`
}
type incompleteDetails struct {
	Reason string         `dd:",+nullable"`
	Extra  map[string]any `dd:",+extra"`
}
type terminalUsage struct {
	InputTokens  int            `dd:",+nullable"`
	OutputTokens int            `dd:",+nullable"`
	TotalTokens  int            `dd:",+nullable"`
	Extra        map[string]any `dd:",+extra"`
}

// itemSlot accumulates one output index's stream. deltaSeen and finalized
// record that argument events arrived, so an explicitly empty delta or
// finalized value is distinguished from no event at all.
type itemSlot struct {
	added          outputItem
	started        bool
	done           json.RawMessage
	previewID      string
	previewName    string
	argumentDelta  strings.Builder
	deltaSeen      bool
	finalArguments string
	finalized      bool
}

// streamed reports whether the slot carries an item the stream told pane
// about; slots created only by text, reasoning, or terminal events do not.
func (s *itemSlot) streamed() bool {
	return s.started || s.done != nil || s.deltaSeen || s.finalized
}

func parseStream(ctx context.Context, body io.Reader, emit func(llm.RoundEvent), identity llm.RoundIdentity, alias, effort string, iteration int) (llm.RoundFinal, error) {
	limit := &io.LimitedReader{R: body, N: maxRoundBytes + 1}
	reader := bufio.NewReader(limit)
	slots := make(map[int]*itemSlot)
	roundID := fmt.Sprintf("%s_%d", callNamespace, roundSequence.Add(1))
	var streamedText strings.Builder
	var terminal *terminalResponse
	var terminalType string
	for terminal == nil {
		if err := ctx.Err(); err != nil {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "cancelled", Err: err}
		}
		line, err := reader.ReadString('\n')
		if limit.N == 0 {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "budget", Reason: "model round exceeds 32 MiB"}
		}
		if err != nil && err != io.EOF {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "transport", Err: err}
		}
		if line == "" && err == io.EOF {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "truncated", Reason: "response ended without a terminal event"}
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			if err == io.EOF {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "truncated", Reason: "response ended without a terminal event"}
			}
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "truncated", Reason: "response ended without a terminal event"}
		}
		tree, e := dd.DecodeStrictJSON([]byte(data))
		if e != nil {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Err: e}
		}
		spansItem, spansOutput, e := opaqueSpans([]byte(data))
		if e != nil {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Err: e}
		}
		var event streamEvent
		if e := dd.Bind(&event, tree, wireBindOpts); e != nil {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Err: e}
		}
		event.Item = spansItem
		if event.Response != nil {
			event.Response.Output = spansOutput
		}
		if event.OutputIndex < 0 {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "negative output index"}
		}
		if event.Type == "error" {
			kind := "upstream"
			code := event.Code
			if event.Error != nil {
				code = event.Error.Code
			}
			if code == "invalid_api_key" || code == "unauthorized" {
				kind = "auth"
			}
			if code == "usage_limit_reached" || code == "rate_limit_exceeded" {
				kind = "allowance"
			}
			return llm.RoundFinal{}, &llm.RoundError{Kind: kind, Reason: "generation failed"}
		}
		slot := slots[event.OutputIndex]
		if slot == nil {
			slot = &itemSlot{}
		}
		switch event.Type {
		case "response.output_item.added":
			if slot.started || len(event.Item) == 0 {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "duplicate or missing output item"}
			}
			item, e := decodeItem(event.Item)
			if e != nil {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Err: e}
			}
			slot.started, slot.added = true, item
			if item.Type == "function_call" {
				slot.previewID = llmCallID(roundID, iteration, event.OutputIndex)
				slot.previewName = item.Name
				emit(llm.RoundEvent{Kind: "tool_call_start", Call: llm.RoundCall{Index: event.OutputIndex, ID: slot.previewID, Name: item.Name}})
			}
		case "response.output_text.delta", "response.refusal.delta":
			if slot.done != nil {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "text after finalized item"}
			}
			streamedText.WriteString(event.Delta)
			emit(llm.RoundEvent{Kind: "delta", Content: event.Delta})
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			emit(llm.RoundEvent{Kind: "thinking_delta", Content: event.Delta})
		case "response.reasoning_summary_part.done":
			emit(llm.RoundEvent{Kind: "thinking_delta", Content: "\n\n"})
		case "response.function_call_arguments.delta":
			if slot.done != nil {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "arguments after finalized item"}
			}
			slot.argumentDelta.WriteString(event.Delta)
			slot.deltaSeen = true
			emit(llm.RoundEvent{Kind: "tool_call_args", Call: llm.RoundCall{Index: event.OutputIndex, ID: slot.previewID, Arguments: event.Delta}})
		case "response.function_call_arguments.done":
			if slot.done != nil {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "arguments after finalized item"}
			}
			if _, ok := tree["arguments"].(string); !ok {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "malformed arguments completion"}
			}
			if slot.finalized && slot.finalArguments != event.Arguments || slot.deltaSeen && slot.argumentDelta.String() != event.Arguments {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "conflicting finalized arguments"}
			}
			slot.finalArguments, slot.finalized = event.Arguments, true
		case "response.output_item.done":
			if slot.done != nil || len(event.Item) == 0 {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "duplicate or missing finalized item"}
			}
			item, e := decodeItem(event.Item)
			if e != nil {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Err: e}
			}
			if reason := reconcileItem(slot, item, true); reason != "" {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: reason}
			}
			slot.done = event.Item
			if item.Type == "function_call" && slot.previewID == "" {
				slot.previewID = llmCallID(roundID, iteration, event.OutputIndex)
				emit(llm.RoundEvent{Kind: "tool_call_start", Call: llm.RoundCall{Index: event.OutputIndex, ID: slot.previewID, Name: item.Name}})
			} else if item.Type == "function_call" && slot.previewName == "" && item.Name != "" {
				emit(llm.RoundEvent{Kind: "tool_call_start", Call: llm.RoundCall{Index: event.OutputIndex, ID: slot.previewID, Name: item.Name}})
			}
		case "response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled":
			if event.Response == nil {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "terminal event has no response"}
			}
			terminal, terminalType = event.Response, event.Type
		}
		slots[event.OutputIndex] = slot
		if err == io.EOF && terminal == nil {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "truncated", Reason: "response ended without a terminal event"}
		}
	}
	if err := ctx.Err(); err != nil {
		return llm.RoundFinal{}, &llm.RoundError{Kind: "cancelled", Err: err}
	}
	if (terminalType == "response.incomplete" || terminalType == "response.done") && terminal.Status == "incomplete" {
		reason := "generation incomplete"
		if terminal.IncompleteDetails != nil && terminal.IncompleteDetails.Reason != "" {
			reason = terminal.IncompleteDetails.Reason
		}
		return llm.RoundFinal{}, &llm.RoundError{Kind: "incomplete", Reason: reason}
	}
	if (terminalType == "response.cancelled" || terminalType == "response.done") && terminal.Status == "cancelled" {
		return llm.RoundFinal{}, &llm.RoundError{Kind: "cancelled", Reason: "generation cancelled"}
	}
	if (terminalType == "response.failed" || terminalType == "response.done") && terminal.Status == "failed" {
		kind := "upstream"
		if terminal.Error != nil {
			kind = classifyProviderCode(terminal.Error.Code)
		}
		return llm.RoundFinal{}, &llm.RoundError{Kind: kind, Reason: "generation failed"}
	}
	if terminal.Status != "completed" || terminalType != "response.completed" && terminalType != "response.done" || terminal.Error != nil || terminal.IncompleteDetails != nil {
		return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "contradictory terminal status"}
	}
	return finishResponse(slots, terminal, streamedText.String(), emit, identity, alias, effort, roundID, iteration)
}

func llmCallID(roundID string, iteration, index int) string {
	return fmt.Sprintf("pane_codex_%s_%d_%d", roundID, iteration, index)
}

// assembledItem is one finalized output item and the output index it holds in
// the provider's response.
type assembledItem struct {
	index int
	raw   json.RawMessage
}

func finishResponse(slots map[int]*itemSlot, terminal *terminalResponse, streamedText string, emit func(llm.RoundEvent), identity llm.RoundIdentity, alias, effort string, roundID string, iteration int) (llm.RoundFinal, error) {
	indices := make([]int, 0, len(slots))
	for index, slot := range slots {
		if slot.streamed() {
			indices = append(indices, index)
		}
	}
	slices.Sort(indices)
	var items []assembledItem
	if len(terminal.Output) > 0 {
		// conservative policy, not live-verified subscription behavior: a
		// non-empty terminal output is the complete ordered item list. every
		// streamed item must appear in it at exactly its output index, so no
		// position is guessed and no unmatched streamed item is appended.
		position := make(map[string]int, len(terminal.Output))
		for pos, raw := range terminal.Output {
			item, err := decodeItem(raw)
			if err != nil || item.ID == "" {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "invalid terminal output item"}
			}
			if _, exists := position[item.ID]; exists {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "invalid terminal output item"}
			}
			position[item.ID] = pos
		}
		streamed := make(map[int]*itemSlot, len(indices))
		for _, index := range indices {
			slot := slots[index]
			id := slot.added.ID
			if slot.done != nil {
				item, _ := decodeItem(slot.done)
				id = item.ID
			}
			pos, ok := position[id]
			if !slot.started && slot.done == nil {
				// an argument-only slot has no item identity; it is held to the
				// terminal item at its own output index.
				pos, ok = index, index < len(terminal.Output)
			}
			if !ok {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "streamed output item missing from terminal output"}
			}
			if pos != index {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "streamed output item position disagrees with terminal output"}
			}
			streamed[pos] = slot
		}
		for pos, terminalRaw := range terminal.Output {
			raw := terminalRaw
			if slot := streamed[pos]; slot != nil && slot.done == nil {
				item, _ := decodeItem(raw)
				if reason := reconcileItem(slot, item, false); reason != "" {
					return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: reason}
				}
			} else if slot != nil {
				merged, err := mergeFinalItem(slot.done, terminalRaw)
				if err != nil {
					return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Err: err}
				}
				raw = merged
			}
			items = append(items, assembledItem{index: pos, raw: raw})
		}
	} else {
		// omitted, null, or empty terminal output leaves the streamed items
		// as the round's output; each streamed item must have finished.
		for _, index := range indices {
			slot := slots[index]
			if slot.done == nil {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "streamed output item never completed"}
			}
			items = append(items, assembledItem{index: index, raw: slot.done})
		}
	}
	used := make(map[string]bool)
	for _, entry := range items {
		item, err := decodeItem(entry.raw)
		if err != nil || used[item.ID] {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "duplicate output item"}
		}
		used[item.ID] = true
	}
	final := llm.RoundFinal{Origin: &llm.RoundOrigin{Alias: alias, Identity: identity, RequestedEffort: effort}}
	continuation := &llm.Continuation{Format: continuationFormat, Version: 1, Identity: identity}
	var text strings.Builder
	seenCalls := make(map[string]bool)
	for position, entry := range items {
		raw := entry.raw
		item, err := decodeItem(raw)
		if err != nil || item.ID == "" || item.Status != "" && item.Status != "completed" {
			return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "invalid finalized output item"}
		}
		switch item.Type {
		case "reasoning":
			if position == len(items)-1 {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "orphaned reasoning item"}
			}
		case "message":
			if item.Role != "assistant" {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "invalid response message role"}
			}
			for _, part := range item.Content {
				if part.Type == "output_text" {
					text.WriteString(part.Text)
				} else if part.Type == "refusal" {
					text.WriteString(part.Refusal)
				} else {
					return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "invalid response content"}
				}
			}
		case "function_call":
			if item.CallID == "" || item.Name == "" || !objectArguments(item.Arguments) || seenCalls[item.CallID] {
				return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "invalid finalized tool call"}
			}
			seenCalls[item.CallID] = true
			// the call id derives from the output index, as the streamed
			// preview id does, so previews and finalized calls agree.
			callID := llmCallID(roundID, iteration, entry.index)
			final.Calls = append(final.Calls, llm.RoundCall{Index: entry.index, ID: callID, Name: item.Name, Arguments: item.Arguments})
			continuation.Bindings = append(continuation.Bindings, llm.CallBinding{PaneCallID: callID, ProviderCallID: item.CallID, ProviderItemID: item.ID})
		default:
			return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "unsupported output item type"}
		}
		continuation.Items = append(continuation.Items, raw)
	}
	if streamedText != "" && streamedText != text.String() {
		return llm.RoundFinal{}, &llm.RoundError{Kind: "protocol", Reason: "streamed and finalized text differ"}
	}
	final.Content = text.String()
	if len(final.Calls) > 0 {
		final.Finish = "tool_calls"
	} else {
		final.Finish = "stop"
	}
	if len(continuation.Items) > 0 {
		final.Continuation = continuation
	}
	if terminal.Usage != nil {
		emit(llm.RoundEvent{Kind: "usage", Usage: &llm.Usage{PromptTokens: terminal.Usage.InputTokens, CompletionTokens: terminal.Usage.OutputTokens, TotalTokens: terminal.Usage.TotalTokens}})
	}
	return final, nil
}

// reconcileItem checks a streamed slot against the completed item that
// finishes it: its own output_item.done (closed) or, when that never arrived,
// the terminal backfill at its output index. it is the one consistency check
// for both paths; ordering and merging stay with finishResponse. observed
// deltas must equal a closed item's arguments exactly, and must be a prefix of
// backfilled arguments; explicitly finalized arguments must always match.
func reconcileItem(slot *itemSlot, item outputItem, closed bool) string {
	if slot.started && (slot.added.ID != "" && slot.added.ID != item.ID || slot.added.Type != "" && slot.added.Type != item.Type || slot.added.CallID != "" && slot.added.CallID != item.CallID || slot.added.Name != "" && slot.added.Name != item.Name) {
		return "output item identity changed"
	}
	if slot.finalized && slot.finalArguments != item.Arguments {
		return "output item arguments changed"
	}
	if slot.deltaSeen {
		delta := slot.argumentDelta.String()
		if closed && delta != item.Arguments || !closed && !strings.HasPrefix(item.Arguments, delta) {
			return "output item arguments changed"
		}
	}
	return ""
}

// mergeFinalItem folds a terminal backfill into a finalized item. a backfill
// that only repeats fields the finalized item already carries returns the
// finalized bytes untouched, so the provider's exact serialization survives;
// only a backfill that adds fields re-encodes the union.
func mergeFinalItem(done, terminal json.RawMessage) (json.RawMessage, error) {
	doneTree, err := dd.DecodeStrictJSON(done)
	if err != nil {
		return nil, err
	}
	terminalTree, err := dd.DecodeStrictJSON(terminal)
	if err != nil {
		return nil, err
	}
	changed := false
	for key, value := range terminalTree {
		old, present := doneTree[key]
		if !present {
			doneTree[key] = value
			changed = true
			continue
		}
		if !jsonValuesEqual(old, value) {
			return nil, fmt.Errorf("finalized item field '%s' changed", key)
		}
	}
	if !changed {
		return done, nil
	}
	return json.Marshal(doneTree)
}

// jsonValuesEqual compares two strictly decoded JSON values. numbers compare
// by exact lexeme, so adjacent integers beyond float64's exact range are
// never mistaken for each other and a changed finalized field is surfaced
// instead of concealed.
func jsonValuesEqual(a, b any) bool {
	if na, ok := a.(json.Number); ok {
		nb, ok := b.(json.Number)
		return ok && na == nb
	}
	switch va := a.(type) {
	case map[string]any:
		vb, ok := b.(map[string]any)
		if !ok || len(va) != len(vb) {
			return false
		}
		for key, av := range va {
			bv, present := vb[key]
			if !present || !jsonValuesEqual(av, bv) {
				return false
			}
		}
		return true
	case []any:
		vb, ok := b.([]any)
		if !ok || len(va) != len(vb) {
			return false
		}
		for i := range va {
			if !jsonValuesEqual(va[i], vb[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

func classifyProviderCode(code string) string {
	switch code {
	case "invalid_api_key", "unauthorized", "invalid_token":
		return "auth"
	case "usage_limit_reached", "rate_limit_exceeded", "insufficient_quota":
		return "allowance"
	default:
		return "upstream"
	}
}
