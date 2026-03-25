package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/michaelquigley/df/dl"
)

func (a *API) handleModels(w http.ResponseWriter, r *http.Request) {
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
