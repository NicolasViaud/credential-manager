package api

import (
	"net/http"
	"wcm/internal/sse"
)

type eventHandler struct {
	broker *sse.Broker
}

// stream handles GET /api/users/{userId}/events
// Returns a persistent SSE stream. The client receives approval_requested,
// approval_resolved, and lock_state_changed events in real time.
func (h *eventHandler) stream(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)
	h.broker.ServeHTTP(w, r, userID)
}
