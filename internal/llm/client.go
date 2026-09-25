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
)

type Client struct {
	httpClient   *http.Client
	baseURL      string
	apiKey       string
	DefaultModel string
	IncludeUsage bool
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
		Model: chatReq.Model, Messages: chatReq.Messages, Stream: chatReq.Stream,
		StreamOptions: chatReq.StreamOptions, MaxTokens: chatReq.MaxTokens,
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
	Model         string
	Messages      []Message
	Tools         []chatWireTool `dd:",+omitempty"`
	Stream        bool
	StreamOptions *StreamOptions `dd:",+omitempty"`
	MaxTokens     int            `dd:",+omitempty"`
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
