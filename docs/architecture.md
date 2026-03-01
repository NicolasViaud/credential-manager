# Architecture

## Overview

The Web Credential Manager (WCM) system replaces GNOME Keyring for headless CDE workspace
containers. It consists of three independent components that communicate over HTTP/REST and SSE.

---

## Component Interactions

```
┌──────────────────────────────────────────────────────────────────────┐
│  Workspace Container (one per workspace)                             │
│                                                                      │
│  ┌─────────────────────────────────┐                                 │
│  │  Linux Applications             │                                 │
│  │  (git, ssh-agent, secret-tool,  │                                 │
│  │   any libsecret consumer)       │                                 │
│  └───────────────┬─────────────────┘                                 │
│                  │ org.freedesktop.secrets (D-Bus session bus)       │
│  ┌───────────────▼─────────────────┐                                 │
│  │  D-Bus Credential Proxy         │                                 │
│  │  ─────────────────────────      │                                 │
│  │  session-id: <random UUID>      │                                 │
│  │  user:       OWNER_EMAIL        │                                 │
│  │  wcm-url:    WCM_BASE_URL       │                                 │
│  └───────────────┬─────────────────┘                                 │
│                  │                                                    │
└──────────────────┼───────────────────────────────────────────────────┘
                   │ REST  /api/users/{email}/...
                   │ (HTTP, no auth in PoC)
                   │
┌──────────────────┼───────────────────────────────────────────────────┐
│  Workspace Container (same or different workspace)                   │
│                                                                      │
│  ┌───────────────▼─────────────────┐                                 │
│  │  VSCode Extension               │◄─── SSE  /api/users/{email}/   │
│  │  (TypeScript)                   │           events                │
│  │  ─────────────────────────      │                                 │
│  │  Renders approval notifications │──── REST POST /approve|deny ──► │
│  │  Shows lock status              │                                 │
│  └─────────────────────────────────┘                                 │
└──────────────────────────────────────────────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────────────────────────┐
│  Web Credential Manager  (single multi-tenant Go service)           │
│                                                                     │
│  ┌──────────────┐  ┌───────────────┐  ┌──────────────────────────┐ │
│  │  API Layer   │  │  Lock Manager │  │  Approval Manager        │ │
│  │  (chi router)│  │               │  │                          │ │
│  │              │  │  session-id → │  │  pending approval →      │ │
│  │  CRUD        │  │  lock state   │  │  SSE broadcast           │ │
│  │  Sessions    │  │  + timeout    │  │  + expiry                │ │
│  │  Approvals   │  └───────────────┘  └──────────────────────────┘ │
│  │  SSE stream  │                                                   │
│  └──────┬───────┘                                                   │
│         │                                                           │
│  ┌──────▼─────────────────────────────────────────────────────────┐ │
│  │  CredentialStore (interface — strategy pattern)                │ │
│  │                                                                │ │
│  │  ┌─────────────────────┐   ┌──────────────┐   ┌────────────┐  │ │
│  │  │  InMemoryStore (PoC)│   │  VaultStore  │   │  DBStore   │  │ │
│  │  │  (current)          │   │  (future)    │   │  (future)  │  │ │
│  │  └─────────────────────┘   └──────────────┘   └────────────┘  │ │
│  └────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────┘
```

---

## WCM Internal Architecture

### Request Flow — Credential Read (happy path, session already unlocked)

```
HTTP GET /api/users/{email}/credentials?service=git&account=alice
    │
    ▼
Router → CredentialsHandler.Search()
    │
    ├── LockManager.IsUnlocked(sessionId) → true
    │
    └── CredentialStore.Search(userId, attributes) → []Credential
    │
    ▼
HTTP 200 [{id, label, secret, ...}]
```

### Request Flow — Credential Read (session locked)

```
HTTP GET /api/users/{email}/credentials?sessionId=X&service=git
    │
    ▼
Router → CredentialsHandler.Search()
    │
    ├── LockManager.IsUnlocked(sessionId) → false
    │
    └── HTTP 423 Locked  { "approvalRequired": true }
```

The proxy receives 423, calls `POST /sessions/{sessionId}/unlock`, then polls
`GET /sessions/{sessionId}/lock` until approved or denied.

### Request Flow — Approval

```
POST /api/users/{email}/sessions/{sessionId}/unlock
    │
    ▼
ApprovalManager.CreateRequest(sessionId, appName) → ApprovalRequest{id, url}
    │
    ├── SSEBroker.Publish(userId, ApprovalEvent{approvalId, appName, url})
    │   └── all connected VSCode extensions for this user receive the event
    │
    └── HTTP 202  { "approvalId": "...", "approvalUrl": "..." }


POST /api/approvals/{approvalId}/approve   (user clicks in browser or VSCode)
    │
    ▼
ApprovalManager.Approve(approvalId)
    │
    └── LockManager.Unlock(sessionId, 10min)
    │
    └── HTTP 204
```

---

## D-Bus Proxy Internal Architecture

### Startup Sequence

```
1. Read OWNER_EMAIL from env           → userId
2. Read WCM_BASE_URL from env          → wcmURL
3. Generate crypto/rand UUID           → sessionId
4. POST /api/users/{email}/sessions    → register session with WCM
5. Connect to D-Bus session bus
6. Register org.freedesktop.secrets service
7. Ready to serve D-Bus calls
```

### GetSecrets Flow (critical path)

```
D-Bus GetSecrets(items, session)
    │
    ├── For each item path, resolve to (service, account, attributes)
    │
    ├── GET /api/users/{email}/credentials?sessionId=X&...
    │   │
    │   ├── 200 OK → return secrets to D-Bus caller
    │   │
    │   └── 423 Locked →
    │           POST /api/users/{email}/sessions/{sessionId}/unlock
    │           → receive approvalUrl
    │           → log approvalUrl (user can open manually if no VSCode)
    │           → poll GET /sessions/{sessionId}/lock every 500ms
    │               until: approved → retry GetSecrets
    │                       denied  → return D-Bus error
    │                       timeout → return D-Bus error
```

---

## User Identity & Multi-Tenancy

The WCM is a multi-tenant service. All user data is isolated by `userId` (the `OWNER_EMAIL`
value). The user identifier is part of every URL path:

```
/api/users/alice@example.com/credentials
/api/users/bob@example.com/credentials
```

The proxy derives `userId` from the `OWNER_EMAIL` environment variable injected by the CDE
platform at workspace startup. This variable identifies the workspace owner.

### Shared Workspace Limitation (PoC)

In a shared workspace, `OWNER_EMAIL` always reflects the workspace owner, not the current
authenticated user. Both users accessing the shared workspace will use the owner's credentials.

Post-PoC resolution: the proxy resolves the current authenticated user dynamically (e.g., from
a platform session token refreshed per login), and routes to the correct WCM user namespace.

---

## SSE Event Stream

The WCM maintains one SSE stream per user:

```
GET /api/users/{email}/events
```

Events are JSON objects with a `type` field:

```
type: approval_requested
{
  "type": "approval_requested",
  "approvalId": "uuid",
  "sessionId": "uuid",
  "applicationName": "git",
  "approvalUrl": "http://wcm.example.com/approve/uuid",
  "expiresAt": "2024-01-01T12:02:00Z"
}

type: approval_expired
{
  "type": "approval_expired",
  "approvalId": "uuid"
}

type: lock_state_changed
{
  "type": "lock_state_changed",
  "sessionId": "uuid",
  "locked": false,
  "expiresAt": "2024-01-01T12:10:00Z"
}
```

Multiple VSCode extension instances (multiple workspaces) can subscribe simultaneously.
All receive the same events. All can approve/deny any pending request.

---

## Technology Stack

| Component | Language | Key Libraries |
|---|---|---|
| WCM | Go | `net/http`, `chi` (router), `encoding/json` |
| D-Bus Proxy | Go | `github.com/godbus/dbus/v5` |
| VSCode Extension | TypeScript | VSCode API, native `EventSource` |

---

## Configuration

### WCM (`cmd/wcm/`)

| Env Var | Default | Description |
|---|---|---|
| `WCM_PORT` | `8080` | HTTP listen port |
| `WCM_LOCK_TIMEOUT` | `10m` | Duration before session re-locks |
| `WCM_APPROVAL_TIMEOUT` | `2m` | Duration before pending approval expires |

### D-Bus Proxy (`cmd/proxy/`)

| Env Var | Required | Description |
|---|---|---|
| `WCM_BASE_URL` | yes | Base URL of the WCM service |
| `OWNER_EMAIL` | yes | Injected by CDE platform; identifies the workspace owner |

### VSCode Extension

Configured via VSCode settings (`settings.json`):

```json
{
  "wcm.baseUrl": "http://wcm.example.com",
  "wcm.userEmail": "alice@example.com"
}
```
