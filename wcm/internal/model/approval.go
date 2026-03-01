package model

import "time"

type ApprovalStatus string

const (
	ApprovalStatusPending  ApprovalStatus = "pending"
	ApprovalStatusApproved ApprovalStatus = "approved"
	ApprovalStatusDenied   ApprovalStatus = "denied"
	ApprovalStatusExpired  ApprovalStatus = "expired"
)

type ApprovalRequest struct {
	ID              string         `json:"id"`
	SessionID       string         `json:"sessionId"`
	UserID          string         `json:"userId"`
	ApplicationName string         `json:"applicationName"`
	WorkspaceID     string         `json:"workspaceId"`
	Status          ApprovalStatus `json:"status"`
	RequestedAt     time.Time      `json:"requestedAt"`
	ExpiresAt       time.Time      `json:"expiresAt"`
}
