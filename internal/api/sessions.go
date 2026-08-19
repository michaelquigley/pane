package api

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/session"
)

type sessionsResponse struct {
	Sessions []session.Summary
}

type sessionErrorResponse struct {
	Error string
}

func (a *API) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	summaries, err := a.sessions.List()
	if err != nil {
		dl.Errorf("listing sessions: %v", err)
		writeSessionError(w, http.StatusInternalServerError, fmt.Sprintf("listing sessions: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = dd.UnbindJSONWriter(sessionsResponse{Sessions: summaries}, w)
}

func (a *API) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	doc, err := a.sessions.Get(id)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrUnsafeID):
			writeSessionError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, fs.ErrNotExist):
			writeSessionError(w, http.StatusNotFound, fmt.Sprintf("session '%s' not found", id))
		case errors.Is(err, session.ErrInvalidDocument):
			dl.Errorf("reading session '%s': %v", id, err)
			writeSessionError(w, http.StatusInternalServerError, fmt.Sprintf("session '%s' is not a valid document", id))
		default:
			dl.Errorf("reading session '%s': %v", id, err)
			writeSessionError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(doc)
}

func (a *API) handleSaveSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// one byte past the cap is enough for the store to reject the body as
	// oversized, and it keeps an unbounded body out of memory.
	doc, err := io.ReadAll(io.LimitReader(r.Body, session.MaxDocumentSize+1))
	if err != nil {
		writeSessionError(w, http.StatusBadRequest, fmt.Sprintf("reading request body: %v", err))
		return
	}

	if err := a.sessions.Save(id, doc); err != nil {
		switch {
		case errors.Is(err, session.ErrOversized):
			writeSessionError(w, http.StatusRequestEntityTooLarge, err.Error())
		case errors.Is(err, session.ErrUnsafeID), errors.Is(err, session.ErrInvalidDocument):
			writeSessionError(w, http.StatusBadRequest, err.Error())
		default:
			// an operational filesystem failure is never a client error.
			dl.Errorf("saving session '%s': %v", id, err)
			writeSessionError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	removed, err := a.sessions.Delete(id)
	if err != nil {
		if errors.Is(err, session.ErrUnsafeID) {
			writeSessionError(w, http.StatusBadRequest, err.Error())
			return
		}
		dl.Errorf("deleting session '%s': %v", id, err)
		writeSessionError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !removed {
		writeSessionError(w, http.StatusNotFound, fmt.Sprintf("session '%s' not found", id))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func writeSessionError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = dd.UnbindJSONWriter(sessionErrorResponse{Error: message}, w)
}
