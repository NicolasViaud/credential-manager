package approval

import (
	"fmt"
	"sync"
	"time"
	"wcm/internal/model"

	"github.com/google/uuid"
)

type Manager struct {
	mu      sync.Mutex
	requests map[string]*model.ApprovalRequest // key: approvalID
	timeout  time.Duration
	baseURL  string
}

func NewManager(timeout time.Duration, baseURL string) *Manager {
	return &Manager{
		requests: make(map[string]*model.ApprovalRequest),
		timeout:  timeout,
		baseURL:  baseURL,
	}
}

// GetOrCreate returns an existing pending approval for the session, or creates a new one.
// created=false means a pending approval already existed (caller should return 409).
func (m *Manager) GetOrCreate(userID, sessionID, workspaceID, appName string) (req *model.ApprovalRequest, created bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for an existing pending approval for this session.
	for _, r := range m.requests {
		if r.SessionID == sessionID && r.UserID == userID && r.Status == model.ApprovalStatusPending {
			if time.Now().Before(r.ExpiresAt) {
				c := *r
				return &c, false, nil
			}
			// Expired in the meantime — mark it and create a new one below.
			r.Status = model.ApprovalStatusExpired
		}
	}

	newReq := &model.ApprovalRequest{
		ID:              uuid.NewString(),
		SessionID:       sessionID,
		UserID:          userID,
		ApplicationName: appName,
		WorkspaceID:     workspaceID,
		Status:          model.ApprovalStatusPending,
		RequestedAt:     time.Now(),
		ExpiresAt:       time.Now().Add(m.timeout),
	}
	m.requests[newReq.ID] = newReq
	c := *newReq
	return &c, true, nil
}

// Get returns a single approval request by ID.
func (m *Manager) Get(approvalID string) (*model.ApprovalRequest, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.requests[approvalID]
	if !ok {
		return nil, false
	}
	// Auto-expire on read.
	if req.Status == model.ApprovalStatusPending && time.Now().After(req.ExpiresAt) {
		req.Status = model.ApprovalStatusExpired
	}
	c := *req
	return &c, true
}

// Approve resolves the approval as approved.
func (m *Manager) Approve(approvalID string) (*model.ApprovalRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.requests[approvalID]
	if !ok {
		return nil, fmt.Errorf("not_found")
	}
	if req.Status == model.ApprovalStatusExpired || time.Now().After(req.ExpiresAt) {
		req.Status = model.ApprovalStatusExpired
		return nil, fmt.Errorf("expired")
	}
	if req.Status != model.ApprovalStatusPending {
		return nil, fmt.Errorf("already_resolved")
	}
	req.Status = model.ApprovalStatusApproved
	c := *req
	return &c, nil
}

// Deny resolves the approval as denied.
func (m *Manager) Deny(approvalID string) (*model.ApprovalRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.requests[approvalID]
	if !ok {
		return nil, fmt.Errorf("not_found")
	}
	if req.Status == model.ApprovalStatusExpired || time.Now().After(req.ExpiresAt) {
		req.Status = model.ApprovalStatusExpired
		return nil, fmt.Errorf("expired")
	}
	if req.Status != model.ApprovalStatusPending {
		return nil, fmt.Errorf("already_resolved")
	}
	req.Status = model.ApprovalStatusDenied
	c := *req
	return &c, nil
}

// ExpireOld marks stale pending approvals as expired and returns them so the
// caller can broadcast SSE events. Intended to be called from a background goroutine.
func (m *Manager) ExpireOld() []*model.ApprovalRequest {
	m.mu.Lock()
	defer m.mu.Unlock()

	var expired []*model.ApprovalRequest
	now := time.Now()
	for _, req := range m.requests {
		if req.Status == model.ApprovalStatusPending && now.After(req.ExpiresAt) {
			req.Status = model.ApprovalStatusExpired
			c := *req
			expired = append(expired, &c)
		}
	}
	return expired
}

// ApprovalURL returns the web URL for the given approval ID.
func (m *Manager) ApprovalURL(approvalID string) string {
	return fmt.Sprintf("%s/approve/%s", m.baseURL, approvalID)
}
