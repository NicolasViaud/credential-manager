package api

import (
	"net/http"
	"wcm/internal/lock"
	"wcm/internal/model"

	"github.com/go-chi/chi/v5"
)

type sessionHandler struct {
	lockMgr *lock.Manager
}

// register handles POST /api/users/{userId}/sessions
func (h *sessionHandler) register(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)

	var body struct {
		SessionID   string `json:"sessionId"`
		WorkspaceID string `json:"workspaceId"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	if body.SessionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "sessionId is required")
		return
	}

	session := h.lockMgr.Register(userID, body.SessionID, body.WorkspaceID)
	writeJSON(w, http.StatusCreated, session)
}

// unregister handles DELETE /api/users/{userId}/sessions/{sessionId}
func (h *sessionHandler) unregister(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)
	sessionID := chi.URLParam(r, "sessionId")

	h.lockMgr.Unregister(userID, sessionID)
	w.WriteHeader(http.StatusNoContent)
}

// getLock handles GET /api/users/{userId}/sessions/{sessionId}/lock
func (h *sessionHandler) getLock(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)
	sessionID := chi.URLParam(r, "sessionId")

	session, ok := h.lockMgr.GetSession(userID, sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "session_not_found", "session not registered")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

// list handles GET /api/users/{userId}/sessions
// Returns all registered sessions for the user — used by the web UI.
func (h *sessionHandler) list(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)
	sessions := h.lockMgr.List(userID)
	if sessions == nil {
		sessions = []*model.Session{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

// forceLock handles POST /api/users/{userId}/sessions/{sessionId}/lock
// Immediately re-locks a session — used by the web UI admin action.
func (h *sessionHandler) forceLock(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)
	sessionID := chi.URLParam(r, "sessionId")

	if !h.lockMgr.Lock(userID, sessionID) {
		writeError(w, http.StatusNotFound, "session_not_found", "session not registered")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
