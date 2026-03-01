# VSCode Extension Specification

## Overview

The VSCode Extension provides in-editor notifications for credential approval requests.
When a Linux application in the workspace requests a credential and the session is locked,
the user receives an Approve / Deny prompt directly in their editor without needing to
switch to a browser.

The extension also displays a persistent lock status indicator in the VSCode status bar.

---

## Architecture

```
VSCode Extension (TypeScript)
│
├── SSEClient                  Maintains a persistent SSE connection to WCM
│   └── /api/users/{email}/events
│
├── NotificationManager        Handles incoming approval events
│   ├── Shows approval notification with Approve / Deny buttons
│   └── Calls WCM REST API on user decision
│
├── StatusBarItem              Shows current lock state for each session
│   └── Updates on lock_state_changed events
│
└── WCMClient                  Thin HTTP client for WCM REST calls
    ├── POST /api/approvals/{id}/approve
    └── POST /api/approvals/{id}/deny
```

---

## SSE Connection

The extension opens one SSE connection per user on activation:

```
GET {wcm.baseUrl}/api/users/{wcm.userEmail}/events
Accept: text/event-stream
Cache-Control: no-cache
```

### Reconnection Strategy

The browser-native `EventSource` API handles reconnection automatically using the
`Last-Event-ID` header. The extension uses a polyfill or `node-fetch` stream for the
Node.js (VSCode extension host) environment since `EventSource` is not available natively.

Reconnection behavior:
- Automatic retry on connection drop
- Exponential backoff: 1s, 2s, 4s, 8s, up to a maximum of 30s
- On reconnect, the `Last-Event-ID` header is sent so WCM can replay missed events

### Connection Status

The extension tracks the SSE connection state and reflects it in the status bar:
- Connected: normal lock state icon
- Disconnected: grey icon with tooltip "WCM: Reconnecting..."

---

## Event Handling

### approval_requested

When this event arrives, the extension shows a VSCode information message with action buttons:

```
┌─────────────────────────────────────────────────────────────┐
│  🔒  git is requesting access to your credentials           │
│      Workspace: ws-996539729659600-0  •  Expires in 1:47    │
│                                                             │
│  [ Approve ]   [ Deny ]   [ Open in Browser ]              │
└─────────────────────────────────────────────────────────────┘
```

Behavior:
- **Approve**: calls `POST /api/approvals/{approvalId}/approve`, dismisses notification
- **Deny**: calls `POST /api/approvals/{approvalId}/deny`, dismisses notification
- **Open in Browser**: opens the `approvalUrl` in the system browser, dismisses notification
- If the notification is dismissed without action (timeout or user closes it): no API call;
  the proxy's approval timeout will eventually expire the request
- If the approval expires while the notification is visible: notification is dismissed
  automatically on receipt of `approval_resolved` with `status: "expired"`

### approval_resolved

- If the approval was resolved by another client (browser or another VSCode instance):
  dismiss the corresponding notification if still visible
- Update status bar if the resolution changed the lock state

### lock_state_changed

- Update the status bar for the affected session
- If `locked: false`: show a brief "Credentials unlocked (10 min)" toast
- If `locked: true`: update status bar icon to locked state

---

## Status Bar

The status bar item is always visible when the extension is active.

### States

| State | Icon | Text | Tooltip |
|---|---|---|---|
| No sessions | `$(key)` | `WCM` | "Credential Manager: No active sessions" |
| All unlocked | `$(unlock)` | `WCM ✓` | "Credentials unlocked • expires in 8:32" |
| Any locked | `$(lock)` | `WCM 🔒` | "Credentials locked • click to open manager" |
| Disconnected | `$(warning)` | `WCM` | "WCM: Disconnected — reconnecting..." |

Clicking the status bar item opens the WCM web UI in a browser.

---

## Configuration

Settings are defined in `package.json` under `contributes.configuration`:

```json
{
  "wcm.baseUrl": {
    "type": "string",
    "default": "http://localhost:8080",
    "description": "Base URL of the Web Credential Manager service"
  },
  "wcm.userEmail": {
    "type": "string",
    "default": "",
    "description": "User email for the WCM event stream. If empty, read from OWNER_EMAIL env var."
  },
  "wcm.approvalNotificationTimeout": {
    "type": "number",
    "default": 0,
    "description": "Seconds before auto-dismissing an approval notification. 0 = never auto-dismiss."
  }
}
```

`wcm.userEmail` falls back to the `OWNER_EMAIL` environment variable, which is already
injected by the CDE platform. No manual configuration is needed in most cases.

---

## Extension Lifecycle

### Activation

The extension activates on VSCode startup (`"activationEvents": ["onStartupFinished"]`).

On activation:
1. Read `wcm.baseUrl` and `wcm.userEmail` (or `OWNER_EMAIL` env var)
2. If either is missing: show a warning and remain inactive
3. Open SSE connection to `{baseUrl}/api/users/{email}/events`
4. Register status bar item
5. Start listening for events

### Deactivation

On deactivation (VSCode shutdown or extension disable):
1. Close the SSE connection
2. Dispose status bar item
3. Dismiss any open approval notifications

---

## File Structure

```
extension/
├── src/
│   ├── extension.ts          # Activation / deactivation entry point
│   ├── sse.ts                # SSE client with reconnection logic
│   ├── client.ts             # WCM REST API client (approve, deny)
│   ├── notification.ts       # Approval notification manager
│   └── statusbar.ts          # Status bar item manager
├── package.json              # Extension manifest, contributes, activationEvents
├── tsconfig.json
└── .vscodeignore
```

---

## Key Implementation Notes

### SSE in Node.js (VSCode Extension Host)

VSCode extensions run in a Node.js environment, not a browser. The native `EventSource` API
is not available. Use `node-fetch` with a readable stream or the `eventsource` npm package:

```typescript
import EventSource from 'eventsource';
const es = new EventSource(url, { headers: { 'Last-Event-ID': lastEventId } });
```

### Notification Deduplication

Track active notifications by `approvalId` in a `Map<string, { notification, timer }>`.
On `approval_resolved`, look up and dismiss the matching notification.

### Multiple Sessions

A user may have multiple workspace sessions simultaneously (multiple workspaces). The
status bar shows the aggregate state: locked if any session is locked, unlocked if all
sessions are unlocked. Hover tooltip lists all sessions individually.

### No Polling

The extension must not poll any WCM endpoint. All state updates arrive via the SSE stream.
The only outbound REST calls are the `approve` and `deny` actions triggered by the user.
