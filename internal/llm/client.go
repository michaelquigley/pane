package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/config"
)

type Client struct {
	httpClient   *http.Client
	baseURL      string
	apiKey       string
	DefaultModel string
	IncludeUsage bool
	profile      string
	effort       string
}

func NewClient(endpoint, model, apiKey string, includeUsage bool) *Client {
	return &Client{
		httpClient:   &http.Client{},
		baseURL:      strings.TrimRight(endpoint, "/"),
		apiKey:       apiKey,
		DefaultModel: model,
		IncludeUsage: includeUsage,
	}
}

// NewQwenClient selects an explicit serving-engine request contract.
func NewQwenClient(endpoint, model, apiKey, profile, effort string, includeUsage bool) (*Client, error) {
	if profile != config.ProfileNinfer && profile != config.ProfileLlamaCPP {
		return nil, fmt.Errorf("unsupported qwen profile '%s'", profile)
	}
	if effort != "" && effort != "none" && effort != "low" && effort != "medium" && effort != "xhigh" {
		return nil, fmt.Errorf("unsupported qwen reasoning effort '%s'", effort)
	}
	c := NewClient(endpoint, model, apiKey, includeUsage)
	c.profile, c.effort = profile, effort
	return c, nil
}

// Origin describes the connection this client resolves for a turn: the
// compatible protocol, upstream model, endpoint, and any profile preset.
func (c *Client) Origin(alias, upstreamModel string) RoundOrigin {
	return RoundOrigin{Alias: alias, RequestedEffort: c.effort, Identity: RoundIdentity{
		Provider: config.ProviderChatCompletions, Protocol: "chat-completions",
		UpstreamModel: upstreamModel, Service: c.baseURL, Profile: c.profile,
	}}
}

func (c *Client) setAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func (c *Client) ListModels(ctx context.Context) (*ModelsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upstream error (status %d): %s", resp.StatusCode, string(body))
	}

	var models ModelsResponse
	if err := dd.BindJSONReader(&models, resp.Body); err != nil {
		return nil, fmt.Errorf("decoding models response: %w", err)
	}

	return &models, nil
}

func (c *Client) StreamChat(ctx context.Context, chatReq *ChatRequest) (*StreamReader, error) {
	chatReq.Stream = true
	if c.IncludeUsage {
		chatReq.StreamOptions = &StreamOptions{IncludeUsage: true}
	}

	request := chatWireRequest{
		Model: chatReq.Model, Stream: chatReq.Stream,
		StreamOptions: chatReq.StreamOptions, MaxTokens: chatReq.MaxTokens,
	}
	for _, message := range chatReq.Messages {
		wire := chatWireMessage{Message: message}
		if message.Role == "assistant" && len(message.ToolCalls) > 0 && chatReq.Profile != "" {
			wire.ReasoningContent = chatReq.LocalReasoning[message.ToolCalls[0].ID]
		}
		request.Messages = append(request.Messages, wire)
	}
	if chatReq.Profile != "" {
		request.ReasoningEffort = chatReq.ReasoningEffort
		if chatReq.Profile == config.ProfileNinfer {
			request.PreserveThinking = new(bool)
		} else {
			request.ChatTemplateKwargs = map[string]any{"preserve_reasoning": false}
			if chatReq.ReasoningEffort == "none" {
				request.Temperature, request.TopP, request.TopK = 0.7, 0.8, 20
			}
		}
	}
	for _, tool := range chatReq.Tools {
		if tool.Function == nil {
			return nil, fmt.Errorf("marshaling chat request: tool has no function")
		}
		_, err := dd.DecodeStrictJSON(tool.Function.Parameters)
		if err != nil {
			return nil, fmt.Errorf("marshaling chat request: invalid tool parameters: %w", err)
		}
		request.Tools = append(request.Tools, chatWireTool{
			Type: tool.Type,
			Function: chatWireFunction{
				Name: tool.Function.Name, Description: tool.Function.Description,
				Parameters: tool.Function.Parameters,
			},
		})
	}
	payload, err := dd.Unbind(request)
	if err != nil {
		return nil, fmt.Errorf("unbinding chat request: %w", err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding unbound chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream unreachable: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upstream error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return NewStreamReader(resp.Body), nil
}

type chatWireRequest struct {
	Model              string
	Messages           []chatWireMessage
	Tools              []chatWireTool `dd:",+omitempty"`
	Stream             bool
	StreamOptions      *StreamOptions `dd:",+omitempty"`
	MaxTokens          int            `dd:",+omitempty"`
	ReasoningEffort    string         `dd:",+omitempty"`
	PreserveThinking   *bool          `dd:",+omitempty"`
	ChatTemplateKwargs map[string]any `dd:",+omitempty"`
	Temperature        float64        `dd:",+omitempty"`
	TopP               float64        `dd:",+omitempty"`
	TopK               int            `dd:",+omitempty"`
}

type chatWireMessage struct {
	Message
	ReasoningContent string
}

func (m chatWireMessage) MarshalDd() (map[string]any, error) {
	// pane's round record and recovery metadata never go upstream; clearing
	// them on a copy before marshaling keeps an unusable stored envelope from
	// blocking the request.
	message := m.Message
	message.Origin, message.Continuation = nil, nil
	message.TurnID, message.RoundID, message.RecoveryPlaceholder = "", "", ""
	payload, err := message.MarshalDd()
	if err != nil {
		return nil, err
	}
	if m.ReasoningContent != "" {
		payload["reasoning_content"] = m.ReasoningContent
	}
	return payload, nil
}

type chatWireTool struct {
	Type     string
	Function chatWireFunction
}

type chatWireFunction struct {
	Name        string
	Description string
	Parameters  json.RawMessage `dd:"-"`
}

func (f chatWireFunction) MarshalDd() (map[string]any, error) {
	return map[string]any{
		"name": f.Name, "description": f.Description,
		"parameters": f.Parameters,
	}, nil
}
