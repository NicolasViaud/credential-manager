# Web Credential Manager

X11-free credential manager for headless CDE workspace containers.
See [PLAN.md](PLAN.md) for the full architecture and roadmap.

## Prerequisites

- Go 1.22+
- Linux (the D-Bus proxy requires Linux; the WCM server runs anywhere)

## Repository Layout

This repository uses a **Go workspace** (`go.work`) with two independent modules:

| Module | Path | Platform | Key deps |
|--------|------|----------|----------|
| `wcm`  | `wcm/` | Any | `chi`, `uuid` |
| `proxy` | `proxy/` | Linux only | `godbus/dbus`, `uuid` |

```
web-credential-manager/
├── go.work                   # workspace — links wcm/ and proxy/
├── Makefile                  # build targets: all, ui, ui-dev, wcm, proxy
├── wcm/                      # Phase 1: WCM server
│   ├── go.mod
│   ├── cmd/wcm/
│   │   ├── main.go
│   │   └── static/           # compiled UI assets (index.html committed, bundle.js built)
│   ├── ui-src/               # TypeScript UI source
│   │   ├── package.json      # esbuild
│   │   └── src/
│   │       ├── main.ts       # SPA entry point
│   │       ├── api.ts        # REST API client + types
│   │       └── sse.ts        # SSE client
│   └── internal/
│       ├── model/            Data types
│       ├── store/            CredentialStore interface + in-memory impl
│       ├── lock/             Per-session lock manager
│       ├── approval/         Approval lifecycle
│       ├── sse/              SSE broker
│       └── api/              HTTP handlers and router
├── proxy/                    # Phase 2: D-Bus proxy (Linux)
│   ├── go.mod
│   ├── cmd/proxy/main.go
│   └── internal/
│       ├── client/           # WCM REST API HTTP client
│       └── dbus/             # Secret Service D-Bus handlers
└── docs/                     # Architecture, API spec, D-Bus spec, security, UI spec
```

## Build & Run

### Install dependencies

```bash
# Fetch deps for each module (run from workspace root)
go mod tidy -C wcm
go mod tidy -C proxy

# Sync the workspace checksum file
go work sync
```

### Build everything (UI + WCM server)

```bash
make all          # builds UI then WCM binary → bin/wcm
```

Or step by step:

```bash
make ui           # compile TypeScript → wcm/cmd/wcm/static/bundle.js
make wcm          # build Go binary    → bin/wcm
```

### Development (UI hot-reload)

```bash
# Terminal 1 — watch mode: recompiles bundle.js on every .ts change
make ui-dev

# Terminal 2 — run server from workspace root
go run ./wcm/cmd/wcm/
```

### D-Bus proxy (Phase 2, Linux only)

```bash
make proxy        # build Go binary → bin/proxy
```

Run the proxy (requires WCM to be running first):

```bash
WCM_BASE_URL=http://localhost:8080 OWNER_EMAIL=alice@example.com ./bin/proxy
```

The proxy logs the approval URL to stderr when a D-Bus caller requests secrets and the
session is locked. Open the URL in a browser or use the web UI to approve.

| Environment Variable    | Default                      | Description                              |
|-------------------------|------------------------------|------------------------------------------|
| `WCM_BASE_URL`          | *(required)*                 | Base URL of the WCM service              |
| `OWNER_EMAIL`           | *(required)*                 | Injected by CDE platform; WCM user ID   |
| `WCM_DBUS_NAME`         | `org.freedesktop.secrets`   | D-Bus service name to register           |
| `WCM_POLL_INTERVAL`     | `500ms`                      | Polling interval while waiting for approval |
| `WCM_APPROVAL_TIMEOUT`  | `2m`                         | Max wait time for user to approve        |

#### Test with secret-tool

```bash
# Store a credential
secret-tool store --label="Test password" service myservice account myaccount

# Retrieve (triggers approval flow if session is locked)
secret-tool lookup service myservice account myaccount

# List matching credentials
secret-tool search service myservice

# Delete
secret-tool clear service myservice account myaccount
```

### Web UI

After the server is running, open: `http://localhost:8080/ui`

On first visit you will be prompted for your user email (`OWNER_EMAIL` value).

### Configuration (WCM server)

| Environment Variable   | Default                 | Description                              |
|------------------------|-------------------------|------------------------------------------|
| `WCM_PORT`             | `8080`                  | HTTP listen port                         |
| `WCM_BASE_URL`         | `http://localhost:8080` | Public base URL (used in approval links) |
| `WCM_LOCK_TIMEOUT`     | `10m`                   | Duration before a session re-locks       |
| `WCM_APPROVAL_TIMEOUT` | `2m`                    | Duration before a pending approval expires |

```bash
WCM_PORT=9090 WCM_BASE_URL=http://wcm.example.com go run ./wcm/cmd/wcm/
```

## Quick Test with curl

Start the server first: `go run ./wcm/cmd/wcm/`

```bash
# 1. Register a session (mimics what the D-Bus proxy does at startup)
curl -s -X POST http://localhost:8080/api/users/alice@example.com/sessions \
  -H "Content-Type: application/json" \
  -d '{"sessionId":"test-session-001","workspaceId":"ws-local"}' | jq

# 2. Store a credential (no lock required for writes)
curl -s -X POST http://localhost:8080/api/users/alice@example.com/credentials \
  -H "Content-Type: application/json" \
  -d '{"label":"GitHub token","secret":"ghp_test123","attributes":{"service":"github.com","account":"alice"}}' | jq

# 3. Try to read — should return 423 (session is locked)
curl -s http://localhost:8080/api/users/alice@example.com/credentials \
  -H "X-Session-Id: test-session-001" | jq

# 4. Request an unlock — returns an approvalId and approvalUrl
curl -s -X POST http://localhost:8080/api/users/alice@example.com/sessions/test-session-001/unlock \
  -H "Content-Type: application/json" \
  -d '{"applicationName":"curl"}' | jq

# 5a. Approve via curl (replace <approvalId> with the value from step 4)
curl -s -X POST http://localhost:8080/api/approvals/<approvalId>/approve

# 5b. Or open the approval page in a browser
#     http://localhost:8080/approve/<approvalId>

# 6. Read credentials — should now succeed and return secrets
curl -s http://localhost:8080/api/users/alice@example.com/credentials \
  -H "X-Session-Id: test-session-001" | jq
```
