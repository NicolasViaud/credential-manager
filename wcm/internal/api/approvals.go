package api

import (
	"net/http"
	"wcm/internal/approval"
	"wcm/internal/lock"
	"wcm/internal/sse"

	"github.com/go-chi/chi/v5"
)

type approvalHandler struct {
	approvalMgr *approval.Manager
	lockMgr     *lock.Manager
	broker      *sse.Broker
}

// requestUnlock handles POST /api/users/{userId}/sessions/{sessionId}/unlock
func (h *approvalHandler) requestUnlock(w http.ResponseWriter, r *http.Request) {
	userID := userIDParam(r)
	sessionID := chi.URLParam(r, "sessionId")

	session, ok := h.lockMgr.GetSession(userID, sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "session_not_found", "session not registered")
		return
	}

	// Already unlocked — nothing to do.
	if !session.Locked {
		writeJSON(w, http.StatusOK, map[string]any{
			"message":   "session is already unlocked",
			"expiresAt": session.ExpiresAt,
		})
		return
	}

	var body struct {
		ApplicationName string `json:"applicationName"`
	}
	// Body is optional — ignore decode errors.
	_ = decodeJSON(r, &body)

	req, created, err := h.approvalMgr.GetOrCreate(userID, sessionID, session.WorkspaceID, body.ApplicationName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	approvalURL := h.approvalMgr.ApprovalURL(req.ID)

	// An approval is already pending for this session.
	if !created {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":       "approval_pending",
			"approvalId":  req.ID,
			"approvalUrl": approvalURL,
			"expiresAt":   req.ExpiresAt,
		})
		return
	}

	// Broadcast the new approval request to all SSE subscribers (VSCode extensions).
	h.broker.Publish(userID, sse.Event{
		ID:   req.ID,
		Type: "approval_requested",
		Data: map[string]any{
			"type":            "approval_requested",
			"approvalId":      req.ID,
			"sessionId":       sessionID,
			"applicationName": req.ApplicationName,
			"workspaceId":     session.WorkspaceID,
			"approvalUrl":     approvalURL,
			"expiresAt":       req.ExpiresAt,
		},
	})

	writeJSON(w, http.StatusAccepted, map[string]any{
		"approvalId":  req.ID,
		"approvalUrl": approvalURL,
		"expiresAt":   req.ExpiresAt,
	})
}

// getApproval handles GET /api/approvals/{approvalId}
func (h *approvalHandler) getApproval(w http.ResponseWriter, r *http.Request) {
	approvalID := chi.URLParam(r, "approvalId")

	req, ok := h.approvalMgr.Get(approvalID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "approval not found")
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// approve handles POST /api/approvals/{approvalId}/approve
func (h *approvalHandler) approve(w http.ResponseWriter, r *http.Request) {
	approvalID := chi.URLParam(r, "approvalId")

	req, err := h.approvalMgr.Approve(approvalID)
	if err != nil {
		switch err.Error() {
		case "not_found":
			writeError(w, http.StatusNotFound, "not_found", "approval not found")
		case "expired":
			writeError(w, http.StatusGone, "approval_expired", "approval request has expired")
		case "already_resolved":
			writeError(w, http.StatusGone, "approval_resolved", "approval already resolved")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}

	// Unlock the session.
	h.lockMgr.Unlock(req.UserID, req.SessionID)

	// Fetch updated session state to include expiresAt in the event.
	session, _ := h.lockMgr.GetSession(req.UserID, req.SessionID)

	h.broker.Publish(req.UserID, sse.Event{
		ID:   req.ID,
		Type: "approval_resolved",
		Data: map[string]any{
			"type":       "approval_resolved",
			"approvalId": req.ID,
			"status":     "approved",
		},
	})

	if session != nil {
		h.broker.Publish(req.UserID, sse.Event{
			Type: "lock_state_changed",
			Data: map[string]any{
				"type":      "lock_state_changed",
				"sessionId": req.SessionID,
				"locked":    false,
				"expiresAt": session.ExpiresAt,
			},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// deny handles POST /api/approvals/{approvalId}/deny
func (h *approvalHandler) deny(w http.ResponseWriter, r *http.Request) {
	approvalID := chi.URLParam(r, "approvalId")

	req, err := h.approvalMgr.Deny(approvalID)
	if err != nil {
		switch err.Error() {
		case "not_found":
			writeError(w, http.StatusNotFound, "not_found", "approval not found")
		case "expired":
			writeError(w, http.StatusGone, "approval_expired", "approval request has expired")
		case "already_resolved":
			writeError(w, http.StatusGone, "approval_resolved", "approval already resolved")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}

	h.broker.Publish(req.UserID, sse.Event{
		ID:   req.ID,
		Type: "approval_resolved",
		Data: map[string]any{
			"type":       "approval_resolved",
			"approvalId": req.ID,
			"status":     "denied",
		},
	})

	w.WriteHeader(http.StatusNoContent)
}

// approvePage serves the approval web UI at GET /approve/{approvalId}.
// The page fetches approval details via JS and calls approve/deny via REST.
const approvePageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Credential Access Request</title>
  <style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body { font-family: system-ui, sans-serif; background: #f5f5f5; display: flex; align-items: center; justify-content: center; min-height: 100vh; }
    .card { background: #fff; border-radius: 8px; box-shadow: 0 2px 12px rgba(0,0,0,.12); padding: 2rem; max-width: 420px; width: 100%; }
    h1 { font-size: 1.2rem; margin-bottom: .5rem; }
    .meta { font-size: .85rem; color: #666; margin-bottom: 1.5rem; line-height: 1.6; }
    .meta b { color: #333; }
    .actions { display: flex; gap: .75rem; }
    button { flex: 1; padding: .7rem 1rem; border: none; border-radius: 6px; font-size: 1rem; cursor: pointer; }
    .btn-approve { background: #2563eb; color: #fff; }
    .btn-approve:hover { background: #1d4ed8; }
    .btn-deny { background: #f3f4f6; color: #374151; border: 1px solid #d1d5db; }
    .btn-deny:hover { background: #e5e7eb; }
    .msg { margin-top: 1rem; font-size: .9rem; padding: .6rem; border-radius: 4px; display: none; }
    .msg.success { background: #d1fae5; color: #065f46; }
    .msg.error   { background: #fee2e2; color: #991b1b; }
    .expiry { font-size: .8rem; color: #888; margin-top: 1.2rem; text-align: center; }
    .spinner { text-align: center; padding: 2rem; color: #888; }
  </style>
</head>
<body>
<div class="card" id="card">
  <div class="spinner" id="spinner">Loading…</div>
  <div id="content" style="display:none">
    <h1>🔒 Credential Access Request</h1>
    <div class="meta">
      <b id="appName">An application</b> is requesting access to your credentials.<br>
      Workspace: <b id="workspaceId">—</b><br>
      Requested: <b id="requestedAt">—</b>
    </div>
    <div class="actions">
      <button class="btn-approve" onclick="resolve('approve')">Approve</button>
      <button class="btn-deny"    onclick="resolve('deny')">Deny</button>
    </div>
    <div class="expiry" id="expiry"></div>
    <div class="msg" id="msg"></div>
  </div>
</div>
<script>
  const approvalId = location.pathname.split('/').pop();

  async function load() {
    try {
      const res = await fetch('/api/approvals/' + approvalId);
      if (!res.ok) { showError(res.status === 404 ? 'Approval not found or already resolved.' : 'Failed to load approval details.'); return; }
      const data = await res.json();
      if (data.status !== 'pending') { showError('This request has already been ' + data.status + '.'); return; }
      document.getElementById('appName').textContent      = data.applicationName || 'An application';
      document.getElementById('workspaceId').textContent  = data.workspaceId     || '—';
      document.getElementById('requestedAt').textContent  = new Date(data.requestedAt).toLocaleTimeString();
      startCountdown(new Date(data.expiresAt));
      document.getElementById('spinner').style.display  = 'none';
      document.getElementById('content').style.display  = 'block';
    } catch(e) { showError('Could not connect to credential manager.'); }
  }

  async function resolve(action) {
    document.querySelectorAll('button').forEach(b => b.disabled = true);
    try {
      const res = await fetch('/api/approvals/' + approvalId + '/' + action, { method: 'POST' });
      const msg = document.getElementById('msg');
      msg.style.display = 'block';
      if (res.status === 204) {
        msg.className = 'msg success';
        msg.textContent = action === 'approve' ? '✓ Access granted for 10 minutes.' : '✗ Access denied.';
      } else {
        const body = await res.json().catch(() => ({}));
        msg.className = 'msg error';
        msg.textContent = body.message || 'Something went wrong.';
        document.querySelectorAll('button').forEach(b => b.disabled = false);
      }
    } catch(e) {
      const msg = document.getElementById('msg');
      msg.style.display = 'block'; msg.className = 'msg error';
      msg.textContent = 'Network error.';
      document.querySelectorAll('button').forEach(b => b.disabled = false);
    }
  }

  function startCountdown(expiresAt) {
    const el = document.getElementById('expiry');
    function tick() {
      const remaining = Math.max(0, Math.floor((expiresAt - Date.now()) / 1000));
      if (remaining === 0) { el.textContent = 'This request has expired.'; return; }
      const m = Math.floor(remaining / 60), s = remaining % 60;
      el.textContent = 'Expires in ' + m + ':' + String(s).padStart(2,'0');
      setTimeout(tick, 1000);
    }
    tick();
  }

  function showError(msg) {
    document.getElementById('spinner').style.display = 'none';
    document.getElementById('card').innerHTML = '<div class="meta" style="text-align:center;padding:1rem">' + msg + '</div>';
  }

  load();
</script>
</body>
</html>`

func (h *approvalHandler) approvePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(approvePageHTML))
}
