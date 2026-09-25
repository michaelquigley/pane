package qwen

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

// Config is one resolved chat-completions connection.
type Config struct {
	Alias         string
	Endpoint      string // e.g. http://host:11400/v1
	APIKey        string // bearer; never logged
	UpstreamModel string
	Profile       string
	Effort        string
	MaxTokens     int // required: qwen charges thinking and answer to one cap

	// Trace, when set, receives each raw SSE data payload.
	Trace func(payload []byte)
}

// Adapter is stateless across rounds.
type Adapter struct {
	cfg       Config
	profile   *Profile
	client    *http.Client
	service   string
	newCallID func() string
}

// New validates the connection and builds the adapter.
func New(cfg Config, base http.RoundTripper) (*Adapter, error) {
	profile, err := Resolve(cfg.Profile, cfg.Effort)
	if err != nil {
		return nil, err
	}
	if profile != nil && cfg.MaxTokens <= 0 {
		return nil, fmt.Errorf("profile '%s' requires an explicit max_tokens", profile.Name)
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid endpoint")
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &Adapter{
		cfg:     cfg,
		profile: profile,
		client: &http.Client{Transport: base, CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirect refused")
		}},
		service:   u.Scheme + "://" + u.Host,
		newCallID: randomCallID,
	}, nil
}

func randomCallID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "pane_call_" + hex.EncodeToString(b[:])
}

func (a *Adapter) Alias() string { return a.cfg.Alias }

func (a *Adapter) Identity() round.Identity {
	id := round.Identity{Provider: "openai-chat-completions", Protocol: "chat-completions", UpstreamModel: a.cfg.UpstreamModel, Service: a.service}
	if a.profile != nil {
		id.Profile = a.profile.Name
	}
	return id
}

type ccMessage struct {
	Role             string       `json:"role"`
	Content          *string      `json:"content"`
	ToolCalls        []ccToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string       `json:"tool_call_id,omitempty"`
	ReasoningContent string       `json:"reasoning_content,omitempty"`
}

type ccToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Index    *int   `json:"index,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type ccTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type ccRequest struct {
	Model              string         `json:"model"`
	Messages           []ccMessage    `json:"messages"`
	Tools              []ccTool       `json:"tools,omitempty"`
	Stream             bool           `json:"stream"`
	StreamOptions      map[string]any `json:"stream_options,omitempty"`
	MaxTokens          int            `json:"max_tokens,omitempty"`
	ReasoningEffort    string         `json:"reasoning_effort,omitempty"`
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
	Temperature        *float64       `json:"temperature,omitempty"`
	TopP               *float64       `json:"top_p,omitempty"`
	TopK               *int           `json:"top_k,omitempty"`
}

// BuildBody converts portable history. the stored history is never mutated:
// recovery placement and reasoning replay exist only in the outgoing body.
func (a *Adapter) BuildBody(req round.Request) ([]byte, error) {
	body := ccRequest{
		Model:         a.cfg.UpstreamModel,
		Stream:        true,
		StreamOptions: map[string]any{"include_usage": true},
		MaxTokens:     a.cfg.MaxTokens,
	}
	p := a.profile
	if p != nil {
		body.ReasoningEffort = a.cfg.Effort
		if len(p.TemplateKwargs) > 0 {
			body.ChatTemplateKwargs = p.TemplateKwargs
		}
		if a.cfg.Effort == "none" && p.NonThinkingSampling != nil {
			s := *p.NonThinkingSampling
			body.Temperature, body.TopP, body.TopK = &s.Temperature, &s.TopP, &s.TopK
		}
	}

	lastUser := -1
	for i, m := range req.History {
		if m.Role == "user" {
			lastUser = i
		}
	}
	identity := a.Identity()
	for i, m := range req.History {
		cm := ccMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				var c ccToolCall
				c.ID, c.Type = tc.ID, "function"
				c.Function.Name, c.Function.Arguments = tc.Name, tc.Arguments
				cm.ToolCalls = append(cm.ToolCalls, c)
			}
			// request-local replay: only this turn's rounds, only reasoning this
			// same connection produced.
			if p != nil && p.ReplayActiveReasoning && i > lastUser && m.Origin != nil && m.Origin.Identity.Equal(identity) {
				if local, ok := req.Local[i]; ok {
					cm.ReasoningContent = local.Reasoning
				}
			}
		}
		body.Messages = append(body.Messages, cm)
	}

	if req.Intent == round.IntentForcedFinal {
		if p != nil && p.ForcedFinalInInitialSystem {
			if len(body.Messages) > 0 && body.Messages[0].Role == "system" {
				merged := strings.TrimSpace(deref(body.Messages[0].Content) + "\n\n" + round.ForcedFinalInstruction)
				first := body.Messages[0]
				first.Content = &merged
				body.Messages[0] = first
			} else {
				body.Messages = append([]ccMessage{{Role: "system", Content: round.Str(round.ForcedFinalInstruction)}}, body.Messages...)
			}
		} else {
			body.Messages = append(body.Messages, ccMessage{Role: "system", Content: round.Str(round.ForcedFinalInstruction)})
		}
	} else {
		for _, t := range req.Tools {
			var ct ccTool
			ct.Type = "function"
			ct.Function.Name, ct.Function.Description, ct.Function.Parameters = t.Name, t.Description, t.Parameters
			body.Tools = append(body.Tools, ct)
		}
	}
	return json.Marshal(body)
}

// Round performs one upstream generation round.
func (a *Adapter) Round(ctx context.Context, req round.Request, emit func(round.Event)) (*round.Final, error) {
	if emit == nil {
		emit = func(round.Event) {}
	}
	payload, err := a.BuildBody(req)
	if err != nil {
		return nil, &round.Error{Kind: round.ErrInvalidRequest, Message: "building request", Err: err}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.Endpoint, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, &round.Error{Kind: round.ErrInvalidRequest, Message: "building request", Err: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	}
	resp, err := a.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &round.Error{Kind: round.ErrCancelled, Message: "round cancelled", Err: err}
		}
		var re *round.Error
		if errors.As(err, &re) {
			return nil, re
		}
		return nil, &round.Error{Kind: round.ErrTransport, Message: err.Error(), Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		kind := round.ErrUpstream
		if resp.StatusCode == http.StatusUnauthorized {
			kind = round.ErrAuth
		} else if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			kind = round.ErrInvalidRequest
		}
		// llama.cpp reports template validation failures as HTTP 500; do not
		// reclassify every 500 as configuration error.
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 400 {
			msg = msg[:400] + "..."
		}
		return nil, &round.Error{Kind: kind, Status: resp.StatusCode, Message: msg}
	}
	return a.readStream(ctx, resp.Body, emit)
}

type ccChunk struct {
	Choices []struct {
		Delta struct {
			Content          *string      `json:"content"`
			Reasoning        *string      `json:"reasoning"`
			ReasoningContent *string      `json:"reasoning_content"`
			ToolCalls        []ccToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

type pendingCall struct {
	seq  int
	name string
	args strings.Builder
}

func (a *Adapter) readStream(ctx context.Context, body io.Reader, emit func(round.Event)) (*round.Final, error) {
	var text, reasoning strings.Builder
	calls := map[int]*pendingCall{}
	finish := ""
	var usage *round.Usage
	sawDone := false

	partial := func() round.Partial {
		pt := round.Partial{Text: text.String()}
		for _, k := range sortedKeys(calls) {
			pt.ToolCalls = append(pt.ToolCalls, round.ToolCall{Name: calls[k].name, Arguments: calls[k].args.String()})
		}
		return pt
	}

	br := bufio.NewReaderSize(body, 64*1024)
	for !sawDone {
		line, err := br.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			if a.cfg.Trace != nil {
				a.cfg.Trace([]byte(data))
			}
			if data == "[DONE]" {
				sawDone = true
				break
			}
			var chunk ccChunk
			if jerr := json.Unmarshal([]byte(data), &chunk); jerr != nil {
				return nil, &round.Error{Kind: round.ErrProtocol, Message: "invalid stream chunk", Partial: partial(), Err: jerr}
			}
			for _, ch := range chunk.Choices {
				d := ch.Delta
				if d.Content != nil && *d.Content != "" {
					text.WriteString(*d.Content)
					emit(round.Event{Kind: round.EventText, Delta: *d.Content})
				}
				r := d.Reasoning
				if r == nil {
					r = d.ReasoningContent
				}
				if r != nil && *r != "" {
					reasoning.WriteString(*r)
					emit(round.Event{Kind: round.EventThinking, Delta: *r})
				}
				for _, tc := range d.ToolCalls {
					idx := 0
					if tc.Index != nil {
						idx = *tc.Index
					}
					pc, ok := calls[idx]
					if !ok {
						pc = &pendingCall{seq: len(calls)}
						calls[idx] = pc
					}
					if tc.Function.Name != "" && pc.name == "" {
						pc.name = tc.Function.Name
						emit(round.Event{Kind: round.EventToolCallStart, Index: pc.seq, Name: pc.name})
					}
					if tc.Function.Arguments != "" {
						pc.args.WriteString(tc.Function.Arguments)
						emit(round.Event{Kind: round.EventToolCallArgs, Index: pc.seq, Delta: tc.Function.Arguments})
					}
				}
				if ch.FinishReason != nil && *ch.FinishReason != "" {
					finish = *ch.FinishReason
				}
			}
			if chunk.Usage != nil {
				usage = &round.Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens, TotalTokens: chunk.Usage.TotalTokens}
				if chunk.Usage.PromptTokensDetails != nil {
					usage.CachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
				}
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, &round.Error{Kind: round.ErrCancelled, Message: "round cancelled", Partial: partial(), Err: ctx.Err()}
			}
			if errors.Is(err, io.EOF) {
				return nil, &round.Error{Kind: round.ErrTruncated, Message: "stream closed before '[DONE]'", Partial: partial()}
			}
			return nil, &round.Error{Kind: round.ErrTransport, Message: err.Error(), Partial: partial(), Err: err}
		}
	}

	if finish == "" {
		return nil, &round.Error{Kind: round.ErrTruncated, Message: "stream ended without a finish reason", Partial: partial()}
	}
	final := &round.Final{RawFinish: finish, Usage: usage}
	identity := a.Identity()
	final.Assistant = round.Message{Role: "assistant", Origin: &round.Origin{Alias: a.cfg.Alias, Effort: a.cfg.Effort, Identity: identity}}
	if s := text.String(); s != "" {
		final.Assistant.Content = &s
	}
	if r := reasoning.String(); r != "" {
		final.Local = &round.Local{Reasoning: r}
	}

	switch finish {
	case "length":
		return nil, &round.Error{Kind: round.ErrIncomplete, Message: "output token limit reached (finish 'length')", Partial: partial()}
	case "content_filter":
		return nil, &round.Error{Kind: round.ErrIncomplete, Message: "content filtered", Partial: partial()}
	}

	var problems []string
	for _, k := range sortedKeys(calls) {
		pc := calls[k]
		args := pc.args.String()
		if args == "" {
			args = "{}"
		}
		var obj map[string]any
		if pc.name == "" || json.Unmarshal([]byte(args), &obj) != nil || obj == nil {
			problems = append(problems, fmt.Sprintf("tool call %d has missing name or malformed arguments", pc.seq))
			continue
		}
		final.Assistant.ToolCalls = append(final.Assistant.ToolCalls, round.ToolCall{ID: a.newCallID(), Name: pc.name, Arguments: args})
	}
	if len(problems) > 0 {
		return nil, &round.Error{Kind: round.ErrIncomplete, Message: strings.Join(problems, "; "), Partial: partial()}
	}
	if len(final.Assistant.ToolCalls) > 0 {
		// finish 'stop' with tool calls is accepted: which reason each engine
		// reports on tool rounds is a live-verification item.
		final.Finish = round.FinishToolCalls
	} else {
		final.Finish = round.FinishStop
		if final.Assistant.Content == nil {
			return nil, &round.Error{Kind: round.ErrIncomplete, Message: "empty response: no content and no tool calls", Partial: partial()}
		}
	}
	return final, nil
}

func sortedKeys(m map[int]*pendingCall) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var _ round.Adapter = (*Adapter)(nil)
