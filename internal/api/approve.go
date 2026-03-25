package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

type ApprovalRegistry struct {
	pending map[string]chan bool
	mu      sync.Mutex
}

func NewApprovalRegistry() *ApprovalRegistry {
	return &ApprovalRegistry{
		pending: make(map[string]chan bool),
	}
}

func (r *ApprovalRegistry) Register(toolCallID string) <-chan bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	ch := make(chan bool, 1)
	r.pending[toolCallID] = ch
	return ch
}

func (r *ApprovalRegistry) Submit(toolCallID string, approved bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ch, ok := r.pending[toolCallID]
	if !ok {
		return fmt.Errorf("no pending approval for tool call %s", toolCallID)
	}

	ch <- approved
	delete(r.pending, toolCallID)
	return nil
}

func (r *ApprovalRegistry) Unregister(toolCallID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pending, toolCallID)
}

type approveRequest struct {
	ID       string `json:"id"`
	Approved bool   `json:"approved"`
}

func (a *API) handleApprove(w http.ResponseWriter, r *http.Request) {
	var req approveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := a.approvals.Submit(req.ID, req.Approved); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
