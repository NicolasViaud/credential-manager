package lock

import (
	"sync"
	"time"
	"wcm/internal/model"
)

type Manager struct {
	mu      sync.Mutex
	sessions map[string]*model.Session // key: sessionID
	timeout time.Duration
}

func NewManager(timeout time.Duration) *Manager {
	return &Manager{
		sessions: make(map[string]*model.Session),
		timeout:  timeout,
	}
}

// Register creates a new session in the locked state.
func (m *Manager) Register(userID, sessionID, workspaceID string) *model.Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	session := &model.Session{
		ID:          sessionID,
		UserID:      userID,
		WorkspaceID: workspaceID,
		Locked:      true,
	}
	m.sessions[sessionID] = session
	s := *session
	return &s
}

// GetSession returns the session, automatically re-locking it if the unlock window has expired.
func (m *Manager) GetSession(userID, sessionID string) (*model.Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok || session.UserID != userID {
		return nil, false
	}

	// Auto-expire: re-lock if the unlock window has passed.
	if !session.Locked && session.ExpiresAt != nil && time.Now().After(*session.ExpiresAt) {
		session.Locked = true
		session.UnlockedAt = nil
		session.ExpiresAt = nil
	}

	s := *session
	return &s, true
}

// IsUnlocked reports whether the session exists and is currently unlocked.
func (m *Manager) IsUnlocked(userID, sessionID string) bool {
	session, ok := m.GetSession(userID, sessionID)
	return ok && !session.Locked
}

// Unlock grants access for the configured timeout duration.
func (m *Manager) Unlock(userID, sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok || session.UserID != userID {
		return false
	}

	now := time.Now()
	expires := now.Add(m.timeout)
	session.Locked = false
	session.UnlockedAt = &now
	session.ExpiresAt = &expires
	return true
}

// Lock force-locks a session immediately (used by the admin UI).
func (m *Manager) Lock(userID, sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok || session.UserID != userID {
		return false
	}
	session.Locked = true
	session.UnlockedAt = nil
	session.ExpiresAt = nil
	return true
}

// List returns copies of all sessions for a user, with auto-expiry applied.
func (m *Manager) List(userID string) []*model.Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	var result []*model.Session
	for _, session := range m.sessions {
		if session.UserID != userID {
			continue
		}
		if !session.Locked && session.ExpiresAt != nil && now.After(*session.ExpiresAt) {
			session.Locked = true
			session.UnlockedAt = nil
			session.ExpiresAt = nil
		}
		s := *session
		result = append(result, &s)
	}
	return result
}

// Unregister removes the session entirely (called on proxy shutdown).
func (m *Manager) Unregister(userID, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if ok && session.UserID == userID {
		delete(m.sessions, sessionID)
	}
}
