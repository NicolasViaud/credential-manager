package api

import (
	"net/http"
	"wcm/internal/lock"
	"wcm/internal/model"
	"wcm/internal/store"

	"github.com/go-chi/chi/v5"
)

type credentialHandler struct {
	store   store.CredentialStore
	lockMgr *lock.Manager
}

// search handles GET /api/users/{userId}/credentials
// X-Session-Id is optional:
//   - Omitted (e.g. UI/admin): returns credentials without lock enforcement.
//   - Provided + session unlocked: returns credentials (D-Bus proxy happy path).
//   - Provided + session locked: returns 423 so the proxy can trigger the approval flow.
func (h *credentialHandler) search(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)
	sessionID := r.Header.Get("X-Session-Id")

	if sessionID != "" {
		session, ok := h.lockMgr.GetSession(userID, sessionID)
		if !ok {
			writeError(w, http.StatusNotFound, "session_not_found", "session not registered; call POST /sessions first")
			return
		}
		if session.Locked {
			writeJSON(w, http.StatusLocked, map[string]any{
				"error":     "session_locked",
				"message":   "Session is locked. Request an unlock first.",
				"sessionId": sessionID,
			})
			return
		}
	}

	// Build attribute filter from query parameters.
	// All query params except "collection" are treated as attribute filters.
	collection := r.URL.Query().Get("collection")
	attrs := make(map[string]string)
	for k, vs := range r.URL.Query() {
		if k != "collection" {
			attrs[k] = vs[0]
		}
	}

	results, err := h.store.Search(userID, collection, attrs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if results == nil {
		results = []*model.Credential{}
	}
	writeJSON(w, http.StatusOK, results)
}

// get handles GET /api/users/{userId}/credentials/{credentialId}
// Same X-Session-Id semantics as search: omit for UI access, provide for proxy access.
func (h *credentialHandler) get(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)
	credID := chi.URLParam(r, "credentialId")
	sessionID := r.Header.Get("X-Session-Id")

	if sessionID != "" {
		session, ok := h.lockMgr.GetSession(userID, sessionID)
		if !ok {
			writeError(w, http.StatusNotFound, "session_not_found", "session not registered")
			return
		}
		if session.Locked {
			writeJSON(w, http.StatusLocked, map[string]any{
				"error":     "session_locked",
				"message":   "Session is locked. Request an unlock first.",
				"sessionId": sessionID,
			})
			return
		}
	}

	cred, err := h.store.Get(userID, credID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "credential not found")
		return
	}
	writeJSON(w, http.StatusOK, cred)
}

// create handles POST /api/users/{userId}/credentials
// Does not require an unlocked session — writing credentials is always allowed.
func (h *credentialHandler) create(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)

	var body struct {
		Collection string            `json:"collection"`
		Label      string            `json:"label"`
		Secret     string            `json:"secret"`
		Attributes map[string]string `json:"attributes"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	if body.Label == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "label is required")
		return
	}
	if body.Secret == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "secret is required")
		return
	}

	cred := &model.Credential{
		UserID:     userID,
		Collection: body.Collection,
		Label:      body.Label,
		Secret:     body.Secret,
		Attributes: body.Attributes,
	}
	if err := h.store.Create(cred); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, cred)
}

// update handles PUT /api/users/{userId}/credentials/{credentialId}
func (h *credentialHandler) update(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)
	credID := chi.URLParam(r, "credentialId")

	existing, err := h.store.Get(userID, credID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "credential not found")
		return
	}

	var body struct {
		Collection *string           `json:"collection"`
		Label      *string           `json:"label"`
		Secret     *string           `json:"secret"`
		Attributes map[string]string `json:"attributes"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}

	if body.Collection != nil {
		existing.Collection = *body.Collection
	}
	if body.Label != nil {
		existing.Label = *body.Label
	}
	if body.Secret != nil {
		existing.Secret = *body.Secret
	}
	if body.Attributes != nil {
		existing.Attributes = body.Attributes
	}

	if err := h.store.Update(existing); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

// delete handles DELETE /api/users/{userId}/credentials/{credentialId}
func (h *credentialHandler) delete(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)
	credID := chi.URLParam(r, "credentialId")

	if err := h.store.Delete(userID, credID); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "credential not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
