package model

import "time"

type Session struct {
	ID          string     `json:"sessionId"`
	UserID      string     `json:"userId"`
	WorkspaceID string     `json:"workspaceId"`
	Locked      bool       `json:"locked"`
	UnlockedAt  *time.Time `json:"unlockedAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}
