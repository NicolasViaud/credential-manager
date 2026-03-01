# Web Credential Manager — Master Plan

## Problem Statement

Traditional Linux credential managers (e.g., GNOME Keyring) depend on an X11 session and a
running D-Bus session daemon. Neither is available inside headless CDE workspace containers.

This project replaces that stack with a web-native credential management system that:
- Requires no X11
- Is compatible with any Linux application that uses `libsecret` / `secret-tool`
- Enforces user-driven access approval without breaking the application's workflow
- Is designed for a shared, multi-user CDE platform

---

## System Overview

Three components work together:

```
┌─────────────────────────────────────────────────────────────────┐
│  Workspace Container                                            │
│                                                                 │
│  ┌──────────────────┐     D-Bus IPC      ┌──────────────────┐  │
│  │  Linux App       │ ──────────────────► │  D-Bus Proxy     │  │
│  │  (git, ssh, etc) │                     │  (Go)            │  │
│  └──────────────────┘                     └────────┬─────────┘  │
│                                                    │ REST        │
│  ┌──────────────────┐     SSE / REST              │             │
│  │  VSCode Extension│ ◄───────────────────────────┤             │
│  └──────────────────┘                             │             │
└───────────────────────────────────────────────────┼─────────────┘
                                                    │
                                                    ▼
                               ┌──────────────────────────────────┐
                               │   Web Credential Manager (Go)    │
                               │   (single multi-tenant service)  │
                               │                                  │
                               │   /api/users/{email}/...         │
                               │                                  │
                               │   - Credential store             │
                               │   - Session & lock model         │
                               │   - Approval flow                │
                               │   - SSE event broker             │
                               │   - Approval web UI              │
                               └──────────────────────────────────┘
```

### Component 1 — Web Credential Manager (WCM)

A single, centralized, multi-tenant Go HTTP service. All users share the same running instance.
User data is isolated by the user identifier (email) in the URL path.

Responsibilities:
- Store and retrieve credentials (strategy pattern — in-memory for PoC)
- Manage per-session lock state with a configurable timeout (default: 10 minutes)
- Drive the approval flow when a locked session requests a credential
- Push approval requests to connected VSCode extensions via SSE
- Serve the approval web page (delegating authentication to the platform)

### Component 2 — D-Bus Credential Proxy

A Go daemon running inside each workspace container. It registers itself as a D-Bus service
implementing the `org.freedesktop.Secret.Service` interface (the standard Secret Service API
used by `libsecret`, `secret-tool`, Git, and most Linux applications that store secrets).

At startup it reads `OWNER_EMAIL` from the environment to determine which user it represents.
All requests to WCM are scoped to that user.

Responsibilities:
- Expose the `org.freedesktop.secrets` D-Bus interface
- Translate D-Bus method calls into WCM REST API calls
- Block D-Bus callers while waiting for the user to approve an unlock
- Track one session ID per proxy instance (cryptographically random UUID)

### Component 3 — VSCode Extension

A TypeScript VSCode extension running inside each workspace. It connects to the WCM SSE stream
to receive real-time notifications when an approval is needed, and lets the user approve or
deny without leaving the editor.

Responsibilities:
- Maintain a persistent SSE connection to WCM
- Display a notification with Approve / Deny actions when a request arrives
- Call the WCM REST API to submit the user's decision
- Show lock status in the VSCode status bar

---

## Key Design Decisions

| Topic | Decision | Rationale |
|---|---|---|
| User identity in proxy | `OWNER_EMAIL` env var | Already injected by CDE platform at workspace startup |
| User scoping in WCM | URL path `/api/users/{email}/...` | Explicit, easy to reason about in PoC |
| Shared workspace | Proxy always routes to workspace owner | Limitation noted; dynamic user resolution deferred post-PoC |
| Credential storage | In-memory, strategy pattern | PoC; Vault/DB backends plug in without changing API |
| Approval granularity | Collection-level (one unlock = all credentials) | Matches GNOME Keyring behavior; avoids per-secret prompt noise |
| Lock scope | Per D-Bus proxy instance (per workspace) | Two workspaces = two independent unlock prompts |
| Lock timeout | 10 minutes (configurable) | Reasonable balance between security and UX |
| Session IDs | `crypto/rand` UUID | Unpredictable; used as lock state key |
| VSCode push | SSE from WCM, REST POST for decisions | SSE is unidirectional push — sufficient; simpler than WebSocket |
| Proxy → WCM auth | None in PoC | Documented in `docs/security.md`; architecture is ready to add it |

---

## Data Models

### Credential

```
id          string    UUID, generated by WCM
userId      string    OWNER_EMAIL value
collection  string    Logical group (e.g. "default") — mirrors Secret Service collections
label       string    Human-readable name
service     string    Service/application name (from libsecret attributes)
account     string    Username or account identifier
secret      string    The plaintext secret (PoC: in-memory only)
attributes  map       Arbitrary key-value pairs for lookup (Secret Service compatible)
createdAt   time
updatedAt   time
```

### Session

```
id           string    UUID, generated by the proxy at startup
userId       string    OWNER_EMAIL value
workspaceId  string    Informational (hostname or injected env var)
locked       bool      True = credential access is not yet approved
unlockedAt   time?     When the last approval was granted
expiresAt    time?     unlockedAt + 10 minutes; after this, locked becomes true again
```

### ApprovalRequest

```
id              string    UUID
sessionId       string    Which session is requesting access
userId          string    Which user's credentials are being requested
applicationName string    D-Bus caller name (e.g. "git", "secret-tool")
status          enum      pending | approved | denied | expired
requestedAt     time
expiresAt       time      requestedAt + 2 minutes (the user has 2 min to respond)
```

---

## Lock & Approval Flow

```
secret-tool            D-Bus Proxy          WCM                   User (browser/VSCode)
     │                      │                 │                             │
     │── GetSecrets ────────►│                 │                             │
     │                      │── GET /lock ───►│                             │
     │                      │◄── locked ──────│                             │
     │                      │                 │                             │
     │                      │── POST /unlock ►│                             │
     │                      │◄── {approvalId} │                             │
     │                      │                 │── SSE event ───────────────►│
     │                      │                 │   (approvalId, appName)     │
     │  (D-Bus call blocks) │                 │                             │
     │                      │── poll /lock ──►│                             │
     │                      │◄── pending ─────│                             │
     │                      │   ...           │                             │
     │                      │                 │◄── POST /approve ───────────│
     │                      │── poll /lock ──►│                             │
     │                      │◄── unlocked ────│                             │
     │◄── secrets ──────────│                 │                             │
     │                      │                 │                             │
```

Key rules:
- While unlocked, subsequent `GetSecrets` calls within the same session skip the approval flow
- After `expiresAt`, the session is locked again; the next request triggers a new approval
- If the user denies, WCM returns a "denied" error to the proxy, which returns an error to the caller
- Approval requests expire after 2 minutes if the user does not respond

---

## Implementation Roadmap

### Phase 1 — Web Credential Manager

| Step | Task | Output |
|---|---|---|
| 1.1 | Go module init, folder structure, config loading | `go.mod`, `cmd/wcm/main.go` |
| 1.2 | `CredentialStore` interface (strategy pattern) | `internal/store/store.go` |
| 1.3 | In-memory store implementing the interface | `internal/store/memory.go` |
| 1.4 | Credential data model | `internal/model/credential.go` |
| 1.5 | Session & lock model + timeout logic | `internal/model/session.go`, `internal/lock/manager.go` |
| 1.6 | Approval model + manager | `internal/model/approval.go`, `internal/approval/manager.go` |
| 1.7 | REST API — credential CRUD endpoints | `internal/api/` |
| 1.8 | REST API — session registration endpoints | `internal/api/` |
| 1.9 | REST API — unlock request + approve/deny endpoints | `internal/api/` |
| 1.10 | SSE broker — push approval events to subscribers | `internal/sse/broker.go` |
| 1.11 | Approval web page (basic HTML) | `web/approve.html` |
| 1.12 | Integration test with `curl` | manual test script |

### Phase 2 — D-Bus Credential Proxy

| Step | Task | Output |
|---|---|---|
| 2.1 | Go module init for proxy | `cmd/proxy/main.go` |
| 2.2 | D-Bus service registration (`org.freedesktop.secrets`) | `internal/dbus/service.go` |
| 2.3 | Implement Secret Service `OpenSession`, `SearchItems` | `internal/dbus/` |
| 2.4 | Implement `GetSecrets` with WCM lock check + unlock flow | `internal/dbus/` |
| 2.5 | Implement `CreateItem`, `DeleteItem` (write-through to WCM) | `internal/dbus/` |
| 2.6 | Session ID generation and registration with WCM | `internal/dbus/` |
| 2.7 | End-to-end test with `secret-tool store` / `secret-tool lookup` | manual test |

### Phase 3 — VSCode Extension

| Step | Task | Output |
|---|---|---|
| 3.1 | Extension scaffold (TypeScript, `vsce`) | `extension/` |
| 3.2 | SSE client — connect to WCM event stream | `extension/src/sse.ts` |
| 3.3 | Approval notification with Approve / Deny buttons | `extension/src/notification.ts` |
| 3.4 | REST client — POST approve/deny to WCM | `extension/src/client.ts` |
| 3.5 | Status bar item showing lock state | `extension/src/statusbar.ts` |
| 3.6 | Configuration (WCM URL, user email) via VSCode settings | `extension/package.json` |

---

## File Structure

```
web-credential-manager/
├── cmd/
│   ├── wcm/
│   │   └── main.go                 # WCM entrypoint
│   └── proxy/
│       └── main.go                 # D-Bus proxy entrypoint
├── internal/
│   ├── store/
│   │   ├── store.go                # CredentialStore interface
│   │   └── memory.go               # In-memory implementation
│   ├── model/
│   │   ├── credential.go
│   │   ├── session.go
│   │   └── approval.go
│   ├── lock/
│   │   └── manager.go              # Session lock state + timeout
│   ├── approval/
│   │   └── manager.go              # Pending approvals lifecycle
│   ├── sse/
│   │   └── broker.go               # SSE fan-out to connected clients
│   └── api/
│       ├── router.go
│       ├── credentials.go          # CRUD handlers
│       ├── sessions.go             # Session handlers
│       ├── approvals.go            # Approve/deny handlers
│       └── events.go               # SSE handler
├── web/
│   └── approve.html                # Approval UI
├── extension/                      # VSCode extension (Phase 3)
│   ├── src/
│   │   ├── extension.ts
│   │   ├── sse.ts
│   │   ├── client.ts
│   │   ├── notification.ts
│   │   └── statusbar.ts
│   └── package.json
├── docs/
│   ├── architecture.md             # Detailed architecture
│   ├── api-spec.md                 # Full REST API reference
│   ├── dbus-spec.md                # D-Bus interface specification
│   ├── security.md                 # Security design (PoC gaps + production plan)
│   └── vscode-spec.md              # VSCode extension specification
├── go.mod
└── go.work                         # Go workspace (wcm + proxy share models)
```

---

## Out of Scope for PoC

- Proxy → WCM authentication (documented in `docs/security.md`)
- Encrypted credential storage
- Dynamic user resolution for shared workspaces
- TLS termination in WCM (assumed: reverse proxy handles it)
- Multi-collection support (single "default" collection only)
- Credential sharing between users
- Audit logging
