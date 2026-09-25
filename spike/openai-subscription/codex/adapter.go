package codex

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

// Config is one resolved subscription connection. there is deliberately no
// endpoint or key field.
type Config struct {
	Alias         string
	UpstreamModel string
	Effort        string

	// MaxOutputTokens is sent as max_output_tokens only when non-zero. no
	// mapping is verified for this route; the spike uses it solely for the
	// approved acceptance probe.
	MaxOutputTokens int

	// Originator is the 'originator' header value. pi sends 'pi', the codex
	// cli 'codex_cli_rs'. the spike defaults to 'pane' and does not
	// impersonate either.
	Originator string
	UserAgent  string

	// Trace, when set, receives each raw SSE data payload. the payloads can
	// carry account-bound continuation and must go to private storage only.
	Trace func(payload []byte)
}

// Adapter is stateless across rounds: everything a round needs arrives in
// its request.
type Adapter struct {
	cfg       Config
	client    *http.Client
	url       string
	creds     Credentials
	newCallID func() string
}

// New builds the production-shaped adapter bound to the fixed destination.
func New(cfg Config, creds Credentials, base http.RoundTripper) (*Adapter, error) {
	return newAdapter(cfg, creds, base, ResponsesURL)
}

// newAdapter lets package tests substitute a local destination. it is not
// reachable from configuration.
func newAdapter(cfg Config, creds Credentials, base http.RoundTripper, url string) (*Adapter, error) {
	if err := ValidateEffort(cfg.UpstreamModel, cfg.Effort); err != nil {
		return nil, err
	}
	if cfg.Originator == "" {
		cfg.Originator = "pane"
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "pane-spike/0"
	}
	return &Adapter{
		cfg:       cfg,
		client:    newLockedClient(base, creds, url),
		url:       url,
		creds:     creds,
		newCallID: randomCallID,
	}, nil
}

func randomCallID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "pane_call_" + hex.EncodeToString(b[:])
}

func (a *Adapter) Alias() string { return a.cfg.Alias }

// Identity resolves the replay identity including the account scope. it
// returns a zero AccountScope if credentials are unavailable.
func (a *Adapter) Identity() round.Identity {
	scope, _ := a.creds.AccountScope(context.Background())
	return a.identity(scope)
}

func (a *Adapter) identity(scope string) round.Identity {
	return round.Identity{
		Provider:      "openai-codex",
		Protocol:      "responses",
		UpstreamModel: a.cfg.UpstreamModel,
		Service:       ServiceIdentity,
		AccountScope:  scope,
	}
}

// BuildBody exposes request conversion for tests and fixture generation.
func (a *Adapter) BuildBody(req round.Request, identity round.Identity) ([]byte, []Decision, error) {
	body, decisions, err := a.buildRequest(req, identity)
	if err != nil {
		return nil, decisions, err
	}
	b, err := json.Marshal(body)
	return b, decisions, err
}

// Round performs one upstream generation round.
func (a *Adapter) Round(ctx context.Context, req round.Request, emit func(round.Event)) (*round.Final, error) {
	scope, err := a.creds.AccountScope(ctx)
	if err != nil {
		return nil, &round.Error{Kind: round.ErrAuth, Message: "not signed in", Err: err}
	}
	identity := a.identity(scope)
	payload, _, err := a.BuildBody(req, identity)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(payload))
	if err != nil {
		return nil, &round.Error{Kind: round.ErrInvalidRequest, Message: "building request", Err: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("OpenAI-Beta", "responses=experimental")
	httpReq.Header.Set("originator", a.cfg.Originator)
	httpReq.Header.Set("User-Agent", a.cfg.UserAgent)
	if req.CacheKey != "" {
		httpReq.Header.Set("session-id", req.CacheKey)
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, classifyTransport(ctx, err, round.Partial{})
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, classifyHTTP(resp)
	}

	p := newParser(emit)
	p.trace = a.cfg.Trace
	if err := readSSE(resp.Body, p); err != nil {
		var re *round.Error
		if errors.As(err, &re) {
			return nil, re
		}
		return nil, classifyTransport(ctx, err, p.partial())
	}
	if ctx.Err() != nil {
		return nil, &round.Error{Kind: round.ErrCancelled, Message: "round cancelled", Partial: p.partial(), Err: ctx.Err()}
	}
	return p.finalize(identity, a.cfg.Alias, a.cfg.Effort, a.newCallID)
}

func classifyTransport(ctx context.Context, err error, partial round.Partial) error {
	var re *round.Error
	if errors.As(err, &re) {
		re.Partial = partial
		return re
	}
	if errors.Is(err, ErrDestination) {
		return &round.Error{Kind: round.ErrDestination, Message: err.Error(), Partial: partial, Err: err}
	}
	if ctx.Err() != nil {
		return &round.Error{Kind: round.ErrCancelled, Message: "round cancelled", Partial: partial, Err: err}
	}
	return &round.Error{Kind: round.ErrTransport, Message: err.Error(), Partial: partial, Err: err}
}

// classifyHTTP maps a non-200 response. bodies are parsed for error codes;
// only a bounded message is retained.
func classifyHTTP(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var parsed struct {
		Error *struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(raw, &parsed)
	code, msg := "", strings.TrimSpace(string(raw))
	if parsed.Error != nil {
		code = firstNonEmpty(parsed.Error.Code, parsed.Error.Type)
		msg = parsed.Error.Message
	} else if parsed.Detail != "" {
		msg = parsed.Detail
	}

	kind := round.ErrUpstream
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		kind = round.ErrAuth
	case resp.StatusCode == http.StatusTooManyRequests:
		kind = classifyCode(code, round.ErrRateLimited)
		if kind != round.ErrAllowance && kind != round.ErrRateLimited {
			kind = round.ErrRateLimited
		}
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound:
		kind = classifyCode(code, round.ErrModelAccess)
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		kind = classifyCode(code, round.ErrInvalidRequest)
	}
	return &round.Error{Kind: kind, Status: resp.StatusCode, Message: sanitizeMessage(code, msg)}
}

var _ round.Adapter = (*Adapter)(nil)

// String avoids accidental credential printing via %v on the adapter.
func (a *Adapter) String() string {
	return fmt.Sprintf("codex adapter '%s' (%s)", a.cfg.Alias, a.cfg.UpstreamModel)
}
