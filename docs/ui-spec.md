# Web Credential Manager UI Specification

## Overview

The WCM UI is a 4th component: a lightweight SPA served directly by the WCM server at `/ui`.
It gives users a browser-based interface to manage their credentials and monitor their active
workspace sessions — no D-Bus proxy or VSCode extension required.

The UI is entirely **client-side rendered** (vanilla HTML/CSS/JS, no build step, no framework).
It is served as a static file from the WCM server alongside the REST API.

---

## Security Boundary

| Operation | UI (browser) | D-Bus Proxy |
|---|---|---|
| List credentials (metadata) | ✓ | ✓ |
| **Read secret value** | **✗ (restricted)** | **✓ only** |
| Create / update / delete credentials | ✓ | ✓ |
| View sessions and lock state | ✓ | ✓ |
| Request unlock (triggers approval flow) | ✓ | ✓ |
| Direct unlock (skip approval flow) | ✓ admin action | ✗ |
| Approve / deny pending requests | ✓ | ✗ |

**Why secrets are restricted to the proxy:** The browser is an untrusted environment.
Any XSS vulnerability, browser extension, or devtools access could leak secrets.
The D-Bus proxy authenticates with a service token (see `security.md`), which a browser
cannot hold securely.

**PoC exception:** The secret column is visible in the UI for development convenience.
A feature flag (`WCM_UI_SHOW_SECRETS=false` by default) controls this. In the PoC it
defaults to `true`.

---

## Authentication

**PoC:** No authentication. On first visit, the user is prompted to enter their email
(matching `OWNER_EMAIL`). This is stored in `localStorage` and used as `{userId}` in all
API calls. There is no session, no cookie, no token.

**Production:** The platform's reverse proxy enforces authentication before the UI is served.
The WCM reads a verified identity header (e.g. `X-Forwarded-User`) and uses it to scope all
API calls automatically — no manual email entry.

---

## Application Layout

```
┌─────────────────────────────────────────────────────┐
│  🔑 Credential Manager          alice@example.com ▼ │  ← top bar
├────────────────┬────────────────────────────────────┤
│                │                                    │
│  Credentials   │   [main panel — switches per tab]  │
│  Sessions      │                                    │
│  Approvals  🔴 │                                    │
│                │                                    │
└────────────────┴────────────────────────────────────┘
```

Three sections in the left nav:
- **Credentials** — CRUD for stored secrets
- **Sessions** — active D-Bus proxy sessions and their lock state
- **Approvals** — pending unlock requests (badge shows count)

---

## Section: Credentials

### List view

Displays all credentials for the current user in a table:

```
┌──────────────────┬──────────────┬──────────────┬────────────┬──────────┐
│ Label            │ Service      │ Account      │ Secret     │ Actions  │
├──────────────────┼──────────────┼──────────────┼────────────┼──────────┤
│ GitHub token     │ github.com   │ alice        │ ••••••• 👁 │ Edit Del │
│ NPM token        │ registry.npm │ alice        │ ••••••• 👁 │ Edit Del │
└──────────────────┴──────────────┴──────────────┴────────────┴──────────┘
                                                   [ + New credential ]
```

- Secret column: masked by default, toggle with 👁 icon
- PoC: shown in plaintext when `WCM_UI_SHOW_SECRETS=true`
- Production: secret column hidden entirely; tooltip: "Secrets are only accessible via the D-Bus proxy"
- Search bar filters by label, service, or account (client-side filter)
- No session ID is sent for the list call in the UI — secrets are returned only if the
  server allows it (see PoC flag)

### Create / Edit modal

```
┌─────────────────────────────────────┐
│ New Credential                    ✕ │
│                                     │
│ Label    [GitHub token           ]  │
│ Secret   [ghp_xxxxx              ]  │
│                                     │
│ Attributes  (key → value pairs)     │
│  service  [github.com            ]  │
│  account  [alice                 ]  │
│  [ + Add attribute ]                │
│                                     │
│ Collection  [default ▼           ]  │
│                                     │
│               [ Cancel ]  [ Save ]  │
└─────────────────────────────────────┘
```

On save: `POST /api/users/{userId}/credentials` (create) or
         `PUT  /api/users/{userId}/credentials/{id}` (edit).

On delete: confirmation dialog → `DELETE /api/users/{userId}/credentials/{id}`.

---

## Section: Sessions

Displays all active D-Bus proxy sessions registered by this user.

```
┌────────────────────┬───────────────────┬───────────────┬─────────────────┐
│ Session ID         │ Workspace         │ Status        │ Actions         │
├────────────────────┼───────────────────┼───────────────┼─────────────────┤
│ a1b2c3d4 (short)  │ ws-9965397-0      │ 🔓 unlocked   │ Lock            │
│                    │                   │ expires 8:32  │                 │
├────────────────────┼───────────────────┼───────────────┼─────────────────┤
│ f7e6d5c4 (short)  │ ws-1234567-1      │ 🔒 locked     │ Unlock          │
└────────────────────┴───────────────────┴───────────────┴─────────────────┘
```

- Countdown timer on unlocked sessions (updated every second via JS)
- **Unlock button**: performs a direct unlock without the approval flow
  (calls `POST /sessions/{id}/unlock` then immediately `POST /approvals/{id}/approve`)
- **Lock button**: calls `POST /sessions/{id}/lock` (new endpoint — see API additions below)
- Session list is updated in real time via the SSE `lock_state_changed` event

### New endpoint required

```
GET /api/users/{userId}/sessions
```

Returns all registered sessions for a user (needed for the sessions list view).
This endpoint does not exist in the current API spec and must be added.

```json
[
  {
    "sessionId": "a1b2c3d4-...",
    "workspaceId": "ws-9965397-0",
    "locked": false,
    "unlockedAt": "2024-01-01T12:00:00Z",
    "expiresAt":  "2024-01-01T12:10:00Z"
  }
]
```

### New endpoint required — Force lock

```
POST /api/users/{userId}/sessions/{sessionId}/lock
```

Immediately re-locks the session. Called by the UI "Lock" button.
PoC: no auth required. Production: requires platform user identity match.

---

## Section: Approvals

Real-time list of pending unlock requests, updated via SSE (`approval_requested` events).

```
┌─────────────────────────────────────────────────────────────────────┐
│  Pending Approval Requests                                          │
├──────────────┬───────────────┬───────────────┬──────────┬──────────┤
│ Application  │ Workspace     │ Requested     │ Expires  │ Actions  │
├──────────────┼───────────────┼───────────────┼──────────┼──────────┤
│ git          │ ws-9965397-0  │ 12:03:45      │ 1:32     │ ✓ ✗     │
└──────────────┴───────────────┴───────────────┴──────────┴──────────┘
                                                   (no pending requests)
```

- ✓ Approve → `POST /api/approvals/{id}/approve` → row disappears, SSE updates sessions panel
- ✗ Deny → `POST /api/approvals/{id}/deny` → row disappears
- Rows disappear automatically on `approval_resolved` SSE events (resolved by another client)
- Expired rows fade out when the countdown reaches 0
- Nav badge shows the count of pending approvals in real time

---

## Real-time Updates (SSE)

The UI opens one SSE connection on load:

```
GET /api/users/{userId}/events
```

Events consumed by the UI:

| Event | UI reaction |
|---|---|
| `approval_requested` | Add row to Approvals panel, increment badge |
| `approval_resolved` | Remove row from Approvals panel, decrement badge |
| `lock_state_changed` | Update session row status and countdown in Sessions panel |

The SSE connection is reconnected automatically with `Last-Event-ID` on disconnect.
Connection status is shown as a small dot in the top bar (green = connected, grey = reconnecting).

---

## Serving the UI

The SPA is a single `ui.html` file served by the WCM Go server at `/ui`.
All API calls use relative URLs (`/api/...`), so no CORS configuration is needed.

```
GET /ui   → serves ui.html
```

The file is embedded in the `wcm` binary using `//go:embed`.

---

## File Structure

```
wcm/
├── cmd/wcm/
│   ├── main.go
│   └── ui.html          ← embedded SPA (single file, no build step)
└── internal/
    └── api/
        └── ui.go        ← handler that serves ui.html
```

---

## API Additions Required

The following endpoints are needed by the UI and are not in the current `api-spec.md`:

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/users/{userId}/sessions` | List all sessions for a user |
| `POST` | `/api/users/{userId}/sessions/{sessionId}/lock` | Force-lock a session immediately |

These must be added to `api-spec.md` and implemented before the UI is built.

---

## Out of Scope for PoC

- User authentication (platform reverse proxy handles this in production)
- Secret masking enforcement (PoC shows secrets with `WCM_UI_SHOW_SECRETS=true`)
- Pagination on the credentials list
- Credential search/filter beyond client-side filtering
- Audit log view
- Multi-user admin view (each user only sees their own data)
