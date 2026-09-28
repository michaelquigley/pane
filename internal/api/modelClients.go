package api

import (
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
)

func NewModelClients(cfg *config.Config) (map[string]*llm.Client, error) {
	if !cfg.HasModelRegistry() {
		return nil, nil
	}

	clients := make(map[string]*llm.Client, len(cfg.Models))
	for _, model := range cfg.ResolvedModels() {
		if model.Provider != config.ProviderChatCompletions {
			continue
		}
		if model.CompatibilityProfile != "" {
			effort := ""
			if model.ReasoningEffort != nil {
				effort = *model.ReasoningEffort
			}
			client, err := llm.NewQwenClient(model.Endpoint, model.UpstreamModel, model.ApiKey, model.CompatibilityProfile, effort, cfg.IncludeUsage)
			if err != nil {
				return nil, err
			}
			clients[model.Alias] = client
		} else {
			clients[model.Alias] = llm.NewClient(model.Endpoint, model.UpstreamModel, model.ApiKey, cfg.IncludeUsage)
		}
	}
	return clients, nil
}
