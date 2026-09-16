package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/llm"
)

func (a *API) handleModels(w http.ResponseWriter, r *http.Request) {
	if a.cfg.HasModelRegistry() {
		models := &llm.ModelsResponse{Object: "list"}
		for _, model := range a.cfg.ResolvedModels() {
			models.Data = append(models.Data, llm.Model{
				ID:      model.Alias,
				Object:  "model",
				OwnedBy: "pane",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = dd.UnbindJSONWriter(models, w)
		return
	}

	models, err := a.llm.ListModels(r.Context())
	if err != nil {
		dl.Errorf("listing models: %v", err)
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "unreachable") {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models)
}
