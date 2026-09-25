package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/mcp"
)

func TestSubscriptionAliasCannotSendBeforeAdapter(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	provider := config.ProviderCodex
	cfg := &config.Config{Endpoint: server.URL, Model: "subscription", Listen: "127.0.0.1:8400", Models: map[string]*config.ModelConfig{"subscription": {Provider: &provider, UpstreamModel: "gpt-5.6-sol"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	a := NewAPI(cfg, llm.NewClient(cfg.Endpoint, cfg.Model, "", false), NewModelClients(cfg), mcp.NewManager(nil), nil)
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"model":"subscription","messages":[{"role":"user","content":"hello"}]}`))
	recorder := httptest.NewRecorder()
	a.handleChat(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || calls != 0 {
		t.Fatalf("subscription dispatched before adapter: status=%d calls=%d", recorder.Code, calls)
	}
}

type upstreamCapture struct {
	mu            sync.Mutex
	calls         int
	authorization string
	request       llm.ChatRequest
}

func (c *upstreamCapture) handler(w http.ResponseWriter, r *http.Request) {
	var request llm.ChatRequest
	if err := dd.BindJSONReader(&request, r.Body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.calls++
	c.authorization = r.Header.Get("Authorization")
	c.request = request
	c.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, "data: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func (c *upstreamCapture) snapshot() (int, string, llm.ChatRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.authorization, c.request
}

func TestRegistryRoutesAliasesThroughProductionClientMap(t *testing.T) {
	elevenCapture := &upstreamCapture{}
	elevenServer := httptest.NewServer(http.HandlerFunc(elevenCapture.handler))
	defer elevenServer.Close()
	fortyfiveCapture := &upstreamCapture{}
	fortyfiveServer := httptest.NewServer(http.HandlerFunc(fortyfiveCapture.handler))
	defer fortyfiveServer.Close()
	unsecuredCapture := &upstreamCapture{}
	unsecuredServer := httptest.NewServer(http.HandlerFunc(unsecuredCapture.handler))
	defer unsecuredServer.Close()

	cfg := &config.Config{
		Endpoint:             elevenServer.URL,
		ApiKey:               "shared-key",
		Model:                "qwen@eleven",
		Listen:               "127.0.0.1:8400",
		ContextWindows:       map[string]int{"qwen@unsecured": 999999},
		DefaultContextWindow: 888888,
		MaxTokens:            map[string]int{"qwen@unsecured": 777777},
		DefaultMaxTokens:     666666,
		Models: map[string]*config.ModelConfig{
			"qwen@eleven": {
				UpstreamModel: "qwen",
				ContextWindow: apiIntPointer(262144),
				MaxTokens:     apiIntPointer(24756),
			},
			"qwen@fortyfive": {
				Endpoint:      apiStringPointer(fortyfiveServer.URL),
				ApiKey:        apiStringPointer("fortyfive-key"),
				UpstreamModel: "qwen",
				ContextWindow: apiIntPointer(163840),
				MaxTokens:     apiIntPointer(12000),
			},
			"qwen@unsecured": {
				Endpoint:      apiStringPointer(unsecuredServer.URL),
				ApiKey:        apiStringPointer(""),
				UpstreamModel: "qwen",
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validating registry fixture: %v", err)
	}

	legacyClient := llm.NewClient(cfg.Endpoint, cfg.Model, cfg.ApiKey, cfg.IncludeUsage)
	modelClients := NewModelClients(cfg)
	a := NewAPI(cfg, legacyClient, modelClients, mcp.NewManager(nil), nil)

	sendRegistryChat(t, a, "qwen@eleven")
	elevenCalls, elevenAuth, elevenRequest := elevenCapture.snapshot()
	fortyfiveCalls, _, _ := fortyfiveCapture.snapshot()
	if elevenCalls != 1 || fortyfiveCalls != 0 {
		t.Fatalf("expected only eleven call, got eleven=%d fortyfive=%d", elevenCalls, fortyfiveCalls)
	}
	if elevenAuth != "Bearer shared-key" || elevenRequest.Model != "qwen" || elevenRequest.MaxTokens != 24756 {
		t.Fatalf("unexpected eleven request: auth=%q request=%#v", elevenAuth, elevenRequest)
	}

	sendRegistryChat(t, a, "qwen@fortyfive")
	fortyfiveCalls, fortyfiveAuth, fortyfiveRequest := fortyfiveCapture.snapshot()
	if fortyfiveCalls != 1 || fortyfiveAuth != "Bearer fortyfive-key" {
		t.Fatalf("unexpected fortyfive routing: calls=%d auth=%q", fortyfiveCalls, fortyfiveAuth)
	}
	if fortyfiveRequest.Model != "qwen" || fortyfiveRequest.MaxTokens != 12000 {
		t.Fatalf("unexpected fortyfive request: %#v", fortyfiveRequest)
	}

	sendRegistryChat(t, a, "qwen@unsecured")
	unsecuredCalls, unsecuredAuth, unsecuredRequest := unsecuredCapture.snapshot()
	if unsecuredCalls != 1 || unsecuredAuth != "" {
		t.Fatalf("expected one unauthenticated call, got calls=%d auth=%q", unsecuredCalls, unsecuredAuth)
	}
	if unsecuredRequest.Model != "qwen" || unsecuredRequest.MaxTokens != 0 {
		t.Fatalf("legacy max token settings leaked into unsecured profile: %#v", unsecuredRequest)
	}

	elevenBefore, _, _ := elevenCapture.snapshot()
	fortyfiveBefore, _, _ := fortyfiveCapture.snapshot()
	unsecuredBefore, _, _ := unsecuredCapture.snapshot()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"model":"unknown","messages":[]}`))
	a.handleChat(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unknown model 'unknown'") {
		t.Fatalf("unexpected unknown-model response: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	elevenAfter, _, _ := elevenCapture.snapshot()
	fortyfiveAfter, _, _ := fortyfiveCapture.snapshot()
	unsecuredAfter, _, _ := unsecuredCapture.snapshot()
	if elevenAfter != elevenBefore || fortyfiveAfter != fortyfiveBefore || unsecuredAfter != unsecuredBefore {
		t.Fatal("unknown alias reached an upstream")
	}
}

func TestRegistryModelsAndConfigComeFromProfiles(t *testing.T) {
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Endpoint:             upstream.URL,
		Model:                "z-model",
		ContextWindows:       map[string]int{"a-model": 999999},
		DefaultContextWindow: 888888,
		Models: map[string]*config.ModelConfig{
			"z-model": {ContextWindow: apiIntPointer(262144)},
			"a-model": {},
			"m-model": {ContextWindow: apiIntPointer(163840)},
		},
	}
	a := NewAPI(cfg, llm.NewClient(upstream.URL, cfg.Model, "", false), NewModelClients(cfg), mcp.NewManager(nil), nil)

	modelsRecorder := httptest.NewRecorder()
	a.handleModels(modelsRecorder, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if upstreamCalls != 0 {
		t.Fatalf("registry model listing contacted upstream %d times", upstreamCalls)
	}
	var models llm.ModelsResponse
	if err := dd.BindJSONReader(&models, modelsRecorder.Body); err != nil {
		t.Fatalf("decoding models: %v", err)
	}
	if len(models.Data) != 3 || models.Data[0].ID != "a-model" || models.Data[1].ID != "m-model" || models.Data[2].ID != "z-model" {
		t.Fatalf("unexpected registry model order: %#v", models.Data)
	}

	configRecorder := httptest.NewRecorder()
	a.handleConfig(configRecorder, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var payload map[string]any
	if err := json.NewDecoder(configRecorder.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding config: %v", err)
	}
	windows, ok := payload["context_windows"].(map[string]any)
	if !ok || len(windows) != 2 || windows["m-model"] != float64(163840) || windows["z-model"] != float64(262144) {
		t.Fatalf("unexpected registry context windows: %#v", payload["context_windows"])
	}
	if _, ok := windows["a-model"]; ok {
		t.Fatalf("legacy context window leaked into omitted profile: %#v", windows)
	}
	if _, ok := payload["default_context_window"]; ok {
		t.Fatalf("registry config exposed legacy default context window: %#v", payload)
	}
	for _, secretField := range []string{"endpoint", "api_key", "upstream_model", "max_tokens"} {
		if _, ok := payload[secretField]; ok {
			t.Fatalf("registry config exposed %s: %#v", secretField, payload)
		}
	}
}

func TestLegacyModelsStillProxyUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"object":"list","data":[{"id":"legacy-model","object":"model","owned_by":"upstream"}]}`)
	}))
	defer upstream.Close()

	cfg := &config.Config{Endpoint: upstream.URL, Model: "legacy-model"}
	a := NewAPI(cfg, llm.NewClient(upstream.URL, cfg.Model, "", false), NewModelClients(cfg), mcp.NewManager(nil), nil)
	recorder := httptest.NewRecorder()
	a.handleModels(recorder, httptest.NewRequest(http.MethodGet, "/api/models", nil))

	var models llm.ModelsResponse
	if err := dd.BindJSONReader(&models, recorder.Body); err != nil {
		t.Fatalf("decoding models: %v", err)
	}
	if len(models.Data) != 1 || models.Data[0].ID != "legacy-model" || models.Data[0].OwnedBy != "upstream" {
		t.Fatalf("unexpected legacy models response: %#v", models)
	}
}

func sendRegistryChat(t *testing.T, a *API, model string) {
	t.Helper()
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}]}`, model)
	recorder := httptest.NewRecorder()
	a.handleChat(recorder, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("chat through %q failed: status=%d body=%q", model, recorder.Code, recorder.Body.String())
	}
}

func apiIntPointer(value int) *int {
	return &value
}

func apiStringPointer(value string) *string {
	return &value
}
