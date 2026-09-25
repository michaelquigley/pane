package api

import (
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
)

func NewModelClients(cfg *config.Config) map[string]*llm.Client {
	if !cfg.HasModelRegistry() {
		return nil
	}

	clients := make(map[string]*llm.Client, len(cfg.Models))
	for _, model := range cfg.ResolvedModels() {
		if model.Provider != config.ProviderChatCompletions {
			continue
		}
		clients[model.Alias] = llm.NewClient(model.Endpoint, model.UpstreamModel, model.ApiKey, cfg.IncludeUsage)
	}
	return clients
}
