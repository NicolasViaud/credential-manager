// Package client provides an HTTP client for the WCM REST API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Credential mirrors the WCM credential JSON model.
type Credential struct {
	ID         string            `json:"id"`
	UserID     string            `json:"userId"`
	Collection string            `json:"collection"`
	Label      string            `json:"label"`
	Secret     string            `json:"secret"`
	Attributes map[string]string `json:"attributes"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

// Session mirrors the WCM session JSON model.
type Session struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	WorkspaceID string     `json:"workspaceId"`
	Locked      bool       `json:"locked"`
	UnlockedAt  *time.Time `json:"unlockedAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}

// UnlockResponse is returned by POST .../unlock (202 or 409).
type UnlockResponse struct {
	ApprovalID  string    `json:"approvalId"`
	ApprovalURL string    `json:"approvalUrl"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// WCMClient is an HTTP client for the WCM REST API.
type WCMClient struct {
	baseURL   string
	userEmail string
	sessionID string
	http      *http.Client
}

// New creates a WCMClient. sessionID is the UUID the proxy generated for itself.
func New(baseURL, userEmail, sessionID string) *WCMClient {
	return &WCMClient{
		baseURL:   baseURL,
		userEmail: userEmail,
		sessionID: sessionID,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

// SessionID returns the WCM session ID used by this client.
func (c *WCMClient) SessionID() string { return c.sessionID }

func (c *WCMClient) userPath() string {
	return fmt.Sprintf("%s/api/users/%s", c.baseURL, url.PathEscape(c.userEmail))
}

// do performs an HTTP request. When withSession is true, X-Session-Id is included
// so that WCM enforces lock state on credential reads.
func (c *WCMClient) do(ctx context.Context, method, rawURL string, body any, withSession bool) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withSession {
		req.Header.Set("X-Session-Id", c.sessionID)
	}
	return c.http.Do(req)
}

func decode[T any](resp *http.Response) (*T, error) {
	defer resp.Body.Close()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return &v, nil
}

// RegisterSession registers the proxy's WCM session at startup.
func (c *WCMClient) RegisterSession(ctx context.Context, workspaceID string) error {
	body := map[string]string{"sessionId": c.sessionID, "workspaceId": workspaceID}
	resp, err := c.do(ctx, http.MethodPost, c.userPath()+"/sessions", body, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("register session: status %d", resp.StatusCode)
	}
	return nil
}

// UnregisterSession removes the WCM session (called on shutdown).
func (c *WCMClient) UnregisterSession(ctx context.Context) error {
	sessURL := fmt.Sprintf("%s/sessions/%s", c.userPath(), c.sessionID)
	resp, err := c.do(ctx, http.MethodDelete, sessURL, nil, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// CheckLock returns true if the session is currently locked.
func (c *WCMClient) CheckLock(ctx context.Context) (bool, error) {
	lockURL := fmt.Sprintf("%s/sessions/%s/lock", c.userPath(), c.sessionID)
	resp, err := c.do(ctx, http.MethodGet, lockURL, nil, false)
	if err != nil {
		return true, err
	}
	sess, err := decode[Session](resp)
	if err != nil {
		return true, err
	}
	return sess.Locked, nil
}

// ForceLock immediately locks the WCM session.
func (c *WCMClient) ForceLock(ctx context.Context) error {
	lockURL := fmt.Sprintf("%s/sessions/%s/lock", c.userPath(), c.sessionID)
	resp, err := c.do(ctx, http.MethodPost, lockURL, nil, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("force lock: status %d", resp.StatusCode)
	}
	return nil
}

// RequestUnlock triggers the approval flow. Returns nil if already unlocked (200 OK).
func (c *WCMClient) RequestUnlock(ctx context.Context, appName string) (*UnlockResponse, error) {
	unlockURL := fmt.Sprintf("%s/sessions/%s/unlock", c.userPath(), c.sessionID)
	body := map[string]string{"applicationName": appName}
	resp, err := c.do(ctx, http.MethodPost, unlockURL, body, false)
	if err != nil {
		return nil, err
	}
	// 200 = already unlocked, no approval needed.
	if resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		return nil, nil
	}
	// 202 = new approval created; 409 = approval already pending.
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusConflict {
		return decode[UnlockResponse](resp)
	}
	defer resp.Body.Close()
	return nil, fmt.Errorf("request unlock: status %d", resp.StatusCode)
}

// PollUntilUnlocked polls the lock state every interval until unlocked or ctx expires.
func (c *WCMClient) PollUntilUnlocked(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			locked, err := c.CheckLock(ctx)
			if err != nil {
				return err
			}
			if !locked {
				return nil
			}
		}
	}
}

// SearchCredentials searches credentials without lock enforcement (omits X-Session-Id).
// Empty attrs returns all credentials in the collection.
func (c *WCMClient) SearchCredentials(ctx context.Context, collection string, attrs map[string]string) ([]*Credential, error) {
	u, _ := url.Parse(c.userPath() + "/credentials")
	q := u.Query()
	if collection != "" {
		q.Set("collection", collection)
	}
	for k, v := range attrs {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	resp, err := c.do(ctx, http.MethodGet, u.String(), nil, false)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, fmt.Errorf("search credentials: status %d", resp.StatusCode)
	}
	creds, err := decode[[]Credential](resp)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*Credential, len(*creds))
	for i := range *creds {
		ptrs[i] = &(*creds)[i]
	}
	return ptrs, nil
}

// GetCredential fetches a credential with lock enforcement (includes X-Session-Id).
// Returns an error string "not_found" or "session_locked" on those conditions.
func (c *WCMClient) GetCredential(ctx context.Context, id string) (*Credential, error) {
	resp, err := c.do(ctx, http.MethodGet, c.userPath()+"/credentials/"+id, nil, true)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		defer resp.Body.Close()
		return nil, fmt.Errorf("not_found")
	case http.StatusLocked:
		defer resp.Body.Close()
		return nil, fmt.Errorf("session_locked")
	case http.StatusOK:
		return decode[Credential](resp)
	default:
		defer resp.Body.Close()
		return nil, fmt.Errorf("get credential: status %d", resp.StatusCode)
	}
}

// GetCredentialNoLock fetches a credential without lock enforcement (omits X-Session-Id).
// Used for reading metadata (label, attributes) when the session may be locked.
func (c *WCMClient) GetCredentialNoLock(ctx context.Context, id string) (*Credential, error) {
	resp, err := c.do(ctx, http.MethodGet, c.userPath()+"/credentials/"+id, nil, false)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		defer resp.Body.Close()
		return nil, fmt.Errorf("not_found")
	case http.StatusOK:
		return decode[Credential](resp)
	default:
		defer resp.Body.Close()
		return nil, fmt.Errorf("get credential (no-lock): status %d", resp.StatusCode)
	}
}

// CreateCredential creates a new credential in WCM.
func (c *WCMClient) CreateCredential(ctx context.Context, label, secret, collection string, attrs map[string]string) (*Credential, error) {
	if attrs == nil {
		attrs = map[string]string{}
	}
	body := map[string]any{
		"label":      label,
		"secret":     secret,
		"collection": collection,
		"attributes": attrs,
	}
	resp, err := c.do(ctx, http.MethodPost, c.userPath()+"/credentials", body, false)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated {
		defer resp.Body.Close()
		return nil, fmt.Errorf("create credential: status %d", resp.StatusCode)
	}
	return decode[Credential](resp)
}

// UpdateCredential updates an existing credential's label, secret, and attributes.
func (c *WCMClient) UpdateCredential(ctx context.Context, id, label, secret string, attrs map[string]string) (*Credential, error) {
	if attrs == nil {
		attrs = map[string]string{}
	}
	body := map[string]any{
		"label":      label,
		"secret":     secret,
		"attributes": attrs,
	}
	resp, err := c.do(ctx, http.MethodPut, c.userPath()+"/credentials/"+id, body, false)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, fmt.Errorf("update credential: status %d", resp.StatusCode)
	}
	return decode[Credential](resp)
}

// DeleteCredential deletes a credential by ID.
func (c *WCMClient) DeleteCredential(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, c.userPath()+"/credentials/"+id, nil, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete credential: status %d", resp.StatusCode)
	}
	return nil
}
