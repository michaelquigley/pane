package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	httpClient   *http.Client
	baseURL      string
	DefaultModel string
}

func NewClient(endpoint, model string) *Client {
	return &Client{
		httpClient:   &http.Client{},
		baseURL:      strings.TrimRight(endpoint, "/"),
		DefaultModel: model,
	}
}

func (c *Client) ListModels(ctx context.Context) (*ModelsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

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
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, fmt.Errorf("decoding models response: %w", err)
	}

	return &models, nil
}

func (c *Client) StreamChat(ctx context.Context, chatReq *ChatRequest) (*StreamReader, error) {
	chatReq.Stream = true

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

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
