package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

// sseEvent is the subset of responses stream events the adapter reads.
type sseEvent struct {
	Type        string          `json:"type"`
	OutputIndex int             `json:"output_index"`
	Delta       string          `json:"delta"`
	Arguments   string          `json:"arguments"`
	Item        json.RawMessage `json:"item"`
	Response    *responseObject `json:"response"`
	Code        string          `json:"code"`
	Message     string          `json:"message"`
	Error       *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type responseObject struct {
	ID                string            `json:"id"`
	Status            string            `json:"status"`
	Output            []json.RawMessage `json:"output"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Usage *struct {
		InputTokens        int `json:"input_tokens"`
		InputTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputTokens        int `json:"output_tokens"`
		OutputTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

type itemHead struct {
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
	EncryptedContent string `json:"encrypted_content"`
}

type slot struct {
	head     itemHead
	text     strings.Builder
	args     strings.Builder
	done     json.RawMessage
	callSeq  int
	startSeq bool
}

// parser accumulates one round. it is single-use.
type parser struct {
	emit     func(round.Event)
	slots    map[int]*slot
	calls    int
	terminal *responseObject
	events   int
	trace    func([]byte)
}

func newParser(emit func(round.Event)) *parser {
	if emit == nil {
		emit = func(round.Event) {}
	}
	return &parser{emit: emit, slots: make(map[int]*slot)}
}

func (p *parser) slotFor(idx int, raw json.RawMessage) *slot {
	s, ok := p.slots[idx]
	if ok {
		return s
	}
	s = &slot{}
	if raw != nil {
		_ = json.Unmarshal(raw, &s.head)
	}
	p.slots[idx] = s
	if s.head.Type == "function_call" {
		s.callSeq = p.calls
		p.calls++
		p.emit(round.Event{Kind: round.EventToolCallStart, Index: s.callSeq, Name: s.head.Name})
	}
	return s
}

// partial reports what was observed so far, for interrupted-turn records.
func (p *parser) partial() round.Partial {
	var part round.Partial
	for _, idx := range p.sortedIndexes() {
		s := p.slots[idx]
		switch s.head.Type {
		case "message":
			part.Text += s.text.String()
		case "function_call":
			part.ToolCalls = append(part.ToolCalls, round.ToolCall{Name: s.head.Name, Arguments: s.args.String()})
		}
	}
	return part
}

func (p *parser) sortedIndexes() []int {
	idx := make([]int, 0, len(p.slots))
	for k := range p.slots {
		idx = append(idx, k)
	}
	sort.Ints(idx)
	return idx
}

// handle consumes one decoded event. a non-nil error ends the round.
func (p *parser) handle(ev sseEvent) error {
	p.events++
	switch ev.Type {
	case "response.output_item.added":
		p.slotFor(ev.OutputIndex, ev.Item)
	case "response.output_text.delta", "response.refusal.delta":
		s := p.slotFor(ev.OutputIndex, nil)
		s.text.WriteString(ev.Delta)
		p.emit(round.Event{Kind: round.EventText, Delta: ev.Delta})
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		p.emit(round.Event{Kind: round.EventThinking, Delta: ev.Delta})
	case "response.reasoning_summary_part.done":
		p.emit(round.Event{Kind: round.EventThinking, Delta: "\n\n"})
	case "response.function_call_arguments.delta":
		s := p.slotFor(ev.OutputIndex, nil)
		s.args.WriteString(ev.Delta)
		p.emit(round.Event{Kind: round.EventToolCallArgs, Index: s.callSeq, Delta: ev.Delta})
	case "response.function_call_arguments.done":
		s := p.slotFor(ev.OutputIndex, nil)
		s.args.Reset()
		s.args.WriteString(ev.Arguments)
		p.emit(round.Event{Kind: round.EventToolCallFrozen, Index: s.callSeq, Name: s.head.Name})
	case "response.output_item.done":
		s := p.slotFor(ev.OutputIndex, ev.Item)
		var head itemHead
		if err := json.Unmarshal(ev.Item, &head); err != nil {
			return &round.Error{Kind: round.ErrProtocol, Message: "malformed output item", Partial: p.partial(), Err: err}
		}
		s.head = head
		s.done = append(json.RawMessage(nil), ev.Item...)
	case "response.completed", "response.done", "response.incomplete":
		if ev.Response == nil {
			return &round.Error{Kind: round.ErrProtocol, Message: "terminal event without response", Partial: p.partial()}
		}
		p.terminal = ev.Response
		if p.terminal.Status == "" && ev.Type == "response.incomplete" {
			p.terminal.Status = "incomplete"
		}
	case "response.failed":
		msg, code := "response failed", ""
		if ev.Response != nil && ev.Response.Error != nil {
			msg, code = ev.Response.Error.Message, ev.Response.Error.Code
		}
		return &round.Error{Kind: classifyCode(code, round.ErrUpstream), Message: sanitizeMessage(code, msg), Partial: p.partial()}
	case "error":
		code, msg := ev.Code, ev.Message
		if ev.Error != nil {
			code, msg = firstNonEmpty(code, ev.Error.Code), firstNonEmpty(msg, ev.Error.Message)
		}
		return &round.Error{Kind: classifyCode(code, round.ErrUpstream), Message: sanitizeMessage(code, msg), Partial: p.partial()}
	}
	return nil
}

// finalize turns a terminal response into a Final or a classified error.
func (p *parser) finalize(identity round.Identity, alias, effort string, newCallID func() string) (*round.Final, error) {
	if p.terminal == nil {
		return nil, &round.Error{Kind: round.ErrTruncated, Message: "stream ended before a terminal response event", Partial: p.partial()}
	}
	t := p.terminal

	// backfill encrypted reasoning from the terminal response when a done
	// item omitted it (pi-ai issue 6409 behavior).
	terminalByID := make(map[string]json.RawMessage)
	for _, raw := range t.Output {
		var h itemHead
		if json.Unmarshal(raw, &h) == nil && h.ID != "" {
			terminalByID[h.ID] = raw
		}
	}

	final := &round.Final{RawFinish: t.Status}
	if t.IncompleteDetails != nil && t.IncompleteDetails.Reason != "" {
		final.RawFinish = t.Status + "." + t.IncompleteDetails.Reason
	}
	if t.Usage != nil {
		u := &round.Usage{InputTokens: t.Usage.InputTokens, OutputTokens: t.Usage.OutputTokens, TotalTokens: t.Usage.TotalTokens}
		if t.Usage.InputTokensDetails != nil {
			u.CachedTokens = t.Usage.InputTokensDetails.CachedTokens
		}
		if t.Usage.OutputTokensDetails != nil {
			u.ReasoningTokens = t.Usage.OutputTokensDetails.ReasoningTokens
		}
		final.Usage = u
	}

	env := &round.Envelope{Version: round.EnvelopeVersion, Format: EnvelopeFormat, Identity: identity}
	var text strings.Builder
	var calls []round.ToolCall
	var problems []string
	for _, idx := range p.sortedIndexes() {
		s := p.slots[idx]
		if s.done == nil {
			problems = append(problems, fmt.Sprintf("output item %d never completed", idx))
			continue
		}
		raw := s.done
		switch s.head.Type {
		case "reasoning":
			if s.head.EncryptedContent == "" {
				if tr, ok := terminalByID[s.head.ID]; ok {
					var th itemHead
					if json.Unmarshal(tr, &th) == nil && th.EncryptedContent != "" {
						raw = tr
					}
				}
			}
		case "message":
			for _, c := range s.head.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				} else if c.Type == "refusal" {
					text.WriteString(c.Refusal)
				}
			}
		case "function_call":
			args := s.head.Arguments
			if !validObjectJSON(args) {
				problems = append(problems, fmt.Sprintf("tool call '%s' has malformed or incomplete arguments", s.head.Name))
				continue
			}
			if s.head.Name == "" || s.head.CallID == "" {
				problems = append(problems, "tool call without name or call id")
				continue
			}
			id := newCallID()
			calls = append(calls, round.ToolCall{ID: id, Name: s.head.Name, Arguments: args})
			env.Bindings = append(env.Bindings, round.Binding{CallID: id, ProviderCallID: s.head.CallID, ProviderItemID: s.head.ID})
		default:
			problems = append(problems, fmt.Sprintf("unsupported output item type '%s'", s.head.Type))
			continue
		}
		env.Items = append(env.Items, raw)
	}

	content := text.String()
	final.Assistant = round.Message{
		Role:      "assistant",
		ToolCalls: calls,
		Origin:    &round.Origin{Alias: alias, Effort: effort, Identity: identity},
	}
	if content != "" {
		final.Assistant.Content = &content
	}
	if len(env.Items) > 0 {
		final.Assistant.Continuation = env
	}

	switch t.Status {
	case "completed":
		if len(problems) > 0 {
			return nil, &round.Error{Kind: round.ErrIncomplete, Message: strings.Join(problems, "; "), Partial: p.partial()}
		}
		if len(calls) > 0 {
			final.Finish = round.FinishToolCalls
		} else {
			final.Finish = round.FinishStop
		}
		if content == "" && len(calls) == 0 {
			return nil, &round.Error{Kind: round.ErrIncomplete, Message: "empty response: no content and no tool calls", Partial: p.partial()}
		}
		return final, nil
	case "incomplete":
		// a truncated round is returned for display but never executable:
		// its tool calls are demoted to partial previews.
		reason := ""
		if t.IncompleteDetails != nil {
			reason = t.IncompleteDetails.Reason
		}
		kind := round.ErrIncomplete
		msg := "response incomplete"
		if reason != "" {
			msg += ": " + reason
		}
		return nil, &round.Error{Kind: kind, Message: msg, Partial: p.partial()}
	default:
		return nil, &round.Error{Kind: round.ErrUpstream, Message: fmt.Sprintf("unexpected terminal status '%s'", t.Status), Partial: p.partial()}
	}
}

func validObjectJSON(s string) bool {
	var v map[string]any
	return json.Unmarshal([]byte(s), &v) == nil && v != nil
}

// readSSE decodes 'data:' frames and feeds them to the parser. EOF without
// a terminal event is left for finalize to classify as truncation.
func readSSE(r io.Reader, p *parser) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		payload := strings.TrimSpace(data.String())
		data.Reset()
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		if p.trace != nil {
			p.trace([]byte(payload))
		}
		var ev sseEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return &round.Error{Kind: round.ErrProtocol, Message: "invalid SSE JSON", Partial: p.partial(), Err: err}
		}
		return p.handle(ev)
	}
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "" {
				if ferr := flush(); ferr != nil {
					return ferr
				}
			} else if strings.HasPrefix(trimmed, "data:") {
				if data.Len() > 0 {
					data.WriteString("\n")
				}
				data.WriteString(strings.TrimPrefix(strings.TrimPrefix(trimmed, "data:"), " "))
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return flush()
			}
			return err
		}
		if p.terminal != nil {
			// the terminal event ends the round; do not wait for the peer.
			return nil
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// classifyCode maps provider error codes to kinds. pi-ai 0.87.1 treats
// usage_limit_reached / usage_not_included / 429 as usage limits.
func classifyCode(code string, fallback round.ErrorKind) round.ErrorKind {
	switch strings.ToLower(code) {
	case "usage_limit_reached", "usage_not_included", "insufficient_quota":
		return round.ErrAllowance
	case "rate_limit_exceeded":
		return round.ErrRateLimited
	case "model_not_found", "model_not_supported":
		return round.ErrModelAccess
	case "invalid_request_error", "invalid_prompt", "context_length_exceeded":
		return round.ErrInvalidRequest
	}
	return fallback
}

// sanitizeMessage bounds provider text; it is never allowed to carry
// headers or tokens because the adapter never places them in errors.
func sanitizeMessage(code, msg string) string {
	s := strings.TrimSpace(msg)
	if code != "" {
		s = code + ": " + s
	}
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
