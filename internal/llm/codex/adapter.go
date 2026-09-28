package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/michaelquigley/pane/internal/auth"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
)

const continuationFormat = "codex-responses-items"

// Credentials supplies account-bound access without exposing credential storage to the adapter.
type Credentials interface {
	CurrentAccount(context.Context) (string, error)
	AccessForAccount(context.Context, string) (string, error)
}

type Adapter struct {
	alias, model, effort, account string
	identity                      llm.RoundIdentity
	credentials                   Credentials
	client                        *auth.LockedClient
}

// New captures the account for one submitted turn; later token rotation stays account-bound.
func New(ctx context.Context, alias, upstreamModel, effort string, credentials Credentials, transport http.RoundTripper) (*Adapter, error) {
	if credentials == nil {
		return nil, errors.New("subscription credentials unavailable")
	}
	if effort != "" {
		valid, known := config.SupportsCodexEffort(upstreamModel, effort)
		if !known || !valid {
			return nil, fmt.Errorf("unsupported reasoning effort '%s' for upstream model '%s'", effort, upstreamModel)
		}
	}
	account, err := credentials.CurrentAccount(ctx)
	if err != nil {
		return nil, err
	}
	if account == "" {
		return nil, auth.ErrLoginRequired
	}
	return &Adapter{
		alias: alias, model: upstreamModel, effort: effort, account: account,
		identity:    llm.RoundIdentity{Provider: config.ProviderCodex, Protocol: "responses", UpstreamModel: upstreamModel, Service: auth.GenerationURL, AccountScope: auth.AccountScope(account)},
		credentials: credentials, client: auth.NewGenerationClient(transport),
	}, nil
}

// Origin is the turn's resolved connection: alias, requested effort, and the
// account-bound replay identity captured when the adapter was created.
func (a *Adapter) Origin() llm.RoundOrigin {
	return llm.RoundOrigin{Alias: a.alias, Identity: a.identity, RequestedEffort: a.effort}
}

func (a *Adapter) Round(ctx context.Context, request llm.RoundRequest, emit func(llm.RoundEvent)) (llm.RoundFinal, error) {
	if request.Model != a.model {
		return llm.RoundFinal{}, &llm.RoundError{Kind: "invalid_request", Reason: "upstream model changed during turn"}
	}
	body, err := a.buildRequest(request)
	if err != nil {
		return llm.RoundFinal{}, &llm.RoundError{Kind: "invalid_request", Err: err}
	}
	if len(body) > 32*1024*1024 {
		return llm.RoundFinal{}, &llm.RoundError{Kind: "budget", Reason: "request exceeds 32 MiB"}
	}
	token, err := a.credentials.AccessForAccount(ctx, a.account)
	if err != nil {
		return llm.RoundFinal{}, &llm.RoundError{Kind: "auth", Err: err}
	}
	if err := ctx.Err(); err != nil {
		return llm.RoundFinal{}, &llm.RoundError{Kind: "cancelled", Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, auth.GenerationURL, bytes.NewReader(body))
	if err != nil {
		return llm.RoundFinal{}, &llm.RoundError{Kind: "invalid_request", Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-ID", a.account)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := a.client.Do(req)
	if err != nil {
		kind := "transport"
		if ctx.Err() != nil {
			kind = "cancelled"
		}
		return llm.RoundFinal{}, &llm.RoundError{Kind: kind, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		kind := "upstream"
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			kind = "auth"
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			kind = "allowance"
		}
		return llm.RoundFinal{}, &llm.RoundError{Kind: kind, Reason: fmt.Sprintf("generation failed (status %d)", resp.StatusCode)}
	}
	return parseStream(ctx, resp.Body, emit, a.identity, a.alias, a.effort, request.Iteration)
}

var _ llm.RoundAdapter = (*Adapter)(nil)
var _ Credentials = (*auth.Manager)(nil)

// requestBody is the Responses generation request pane sends. input holds
// wire trees: dd-unbound maps for constructed items and strict-decoded trees
// for compatible continuation items, so the final encoding serializes both
// through one substrate. store, stream, instructions, and include are always
// present; tools, tool_choice, parallel_tool_calls, and reasoning are omitted
// when unset.
type requestBody struct {
	Model             string
	Store             bool
	Stream            bool
	Instructions      string
	Input             []any
	Tools             []toolDef `dd:",+omitempty"`
	ToolChoice        string    `dd:",+omitempty"`
	ParallelToolCalls *bool
	Reasoning         *reasoning
	Include           []string
}

type reasoning struct {
	Effort  string
	Summary string
}

type toolDef struct {
	Type        string
	Name        string
	Description string
	Parameters  map[string]any
	Strict      bool
}
