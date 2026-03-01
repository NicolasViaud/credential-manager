# Security Design

This document describes the security model of the Web Credential Manager system, the gaps
that exist in the PoC, and the design for a production-hardened implementation.

---

## PoC Security Posture

The PoC is intentionally minimal. The following security controls are **not implemented**:

| Control | Status | Risk |
|---|---|---|
| Proxy → WCM authentication | Not implemented | Any process that can reach WCM can call the API |
| TLS on WCM | Not implemented | Credentials in transit are in cleartext |
| Credential encryption at rest | Not implemented | Credentials are plaintext in memory |
| Approval page authentication | Delegated to platform | Safe if platform auth is in place |
| Session ID entropy | Implemented | `crypto/rand` UUID — safe |
| Process isolation for WCM | Not implemented | `/proc` memory readable by same-user processes |

The PoC is safe to use in a trusted local environment (e.g., developer laptop, internal
staging). It must not be used to store production secrets or be exposed to untrusted networks.

---

## Threat Model

### Assets

1. **Stored credentials** — the secrets themselves (API tokens, passwords, SSH keys)
2. **Approval flow** — the mechanism that gates credential access
3. **Session lock state** — determines whether credentials can be read without re-prompting

### Threat Actors

| Actor | Description |
|---|---|
| Rogue process in workspace | A process running as the workspace user, attempting to read other users' credentials or bypass the approval flow |
| Network attacker | An attacker on the same network as the WCM service |
| Workspace user (over-privileged) | A legitimate user attempting to access another user's credentials |

### Trust Boundaries

```
[Linux App] → [D-Bus session bus] → [D-Bus Proxy] → [HTTP] → [WCM]
                                                               ↑
                                                    [Approval Web Page]
                                                    [VSCode Extension]
```

The D-Bus session bus is already user-scoped (only processes of the same UID can connect to
a session bus). This provides natural isolation at the D-Bus layer.

The HTTP boundary between proxy and WCM is the main attack surface in the current design.

---

## Production Authentication Design (Not Implemented in PoC)

### Goal

Ensure that only a legitimate D-Bus proxy instance (running in a valid, platform-provisioned
workspace for the correct user) can call the sensitive WCM credential read endpoints.

### Two Orthogonal Trust Levels: Workload Identity + User Identity

This design uses two separate tokens operating at different trust levels — a pattern also
found in Kubernetes (service account + kubeconfig), AWS (EC2 instance role + IAM user),
and GCP (Workload Identity + Google account).

| | Service Token (file) | User Token (platform session) |
|---|---|---|
| **Answers** | "Is this a legitimate proxy?" | "Which user is acting?" |
| **Authenticates** | The workload (the proxy process) | The human user |
| **Changes when** | Workspace is reprovisioned | A different user logs in |
| **Shared filesystem risk** | Readable by any workspace user | Each user has their own token |
| **Scope** | Workspace-level | User-level |

**Why the shared filesystem is not a problem for credential isolation:**

In a shared workspace, User B can read the service token file from the shared filesystem.
But even with the stolen service token, User B must also supply a user token — and the only
user token they possess is their own. WCM uses the user token to determine whose credentials
to serve, so User B's request is routed to User B's credentials, not User A's.

To reach User A's credentials, User B would need User A's user token. That token is signed
by the platform's private key, which User B cannot access. Credential isolation is therefore
fully enforced by the user token — the service token only proves the request came from a
legitimate workspace proxy, not an arbitrary external caller.

### Approach: Platform-Issued Workspace Token

The CDE platform already issues tokens to workspace sessions. The proposed approach leverages
this existing infrastructure:

**Step 1 — Token injection at workspace startup**

The platform generates a short-lived, workspace-scoped service token when a workspace
container starts. This token is written to a file with restricted permissions:

```
/run/wcm/proxy-token        (mode 0400, owned by the proxy service user)
```

The token encodes:
- `userId` (OWNER_EMAIL)
- `workspaceId`
- `issuedAt` / `expiresAt`
- `scope: ["wcm:read", "wcm:write"]`
- Signed by the platform's private key

**Step 2 — Proxy reads and uses the token**

The D-Bus proxy reads the token file at startup and sends it in every WCM request:

```
Authorization: Bearer <token>
```

**Step 3 — WCM validates the token**

WCM validates the token on every request:
1. Verify the signature against the platform's public key
2. Check `expiresAt`
3. Verify `userId` matches the URL path `{userId}`
4. Verify `scope` includes the required permission

**Step 4 — Token rotation**

The platform refreshes the token before it expires (e.g., every 55 minutes for a 1-hour
token). The proxy watches the token file for changes using `inotify` and reloads it.

### Why not a static API key?

Static API keys have no expiry and are difficult to rotate at scale. A platform-issued token:
- Is automatically scoped to the correct user
- Expires automatically
- Can be revoked by the platform without restarting WCM

### Why not mTLS?

mTLS is an alternative but requires:
- A CA that WCM trusts
- Certificate issuance for each workspace
- Certificate rotation infrastructure

This is more complex than leveraging the existing platform token system.

### Threat Remaining After Auth Implementation

Even with token-based auth, a malicious process running as the same workspace user can:
1. Read the token file (same UID → same file permissions)
2. Use the token to call WCM directly

Mitigation options (future work):
- Run the proxy as a dedicated service user (different UID from workspace user)
- Use a Unix domain socket instead of TCP for proxy → WCM communication, with socket peer
  credential validation (`SO_PEERCRED`) to verify the proxy's UID
- Use Linux namespaces to isolate the proxy's network namespace from the workspace user

---

## Approval Flow Security

### Current PoC

The approval web page at `/approve/{approvalId}` has no authentication in the PoC. Anyone
who obtains the URL can approve or deny the request.

In practice, the URL is only exposed via:
- The VSCode extension (which runs inside the authenticated workspace)
- The proxy's stderr log (visible only to workspace users)

This is acceptable for a PoC on a trusted network.

### Production Design

The approval page must be protected by the platform's authentication system:

1. The platform's reverse proxy (nginx, Traefik, etc.) handles authentication before
   forwarding requests to WCM
2. WCM receives a verified user identity header (e.g., `X-Forwarded-User: alice@example.com`)
3. WCM validates that the authenticated user matches the `userId` on the approval request
4. If they don't match: return `403 Forbidden`

Additionally, approval tokens should be:
- Single-use (invalidated immediately after use)
- Short-lived (2 minute expiry, already implemented)
- Tied to the user identity (validated against the platform session)

---

## Session ID Security

Session IDs are generated using `crypto/rand` in the proxy:

```go
import "crypto/rand"
import "github.com/google/uuid"

sessionId, err := uuid.NewRandom()  // uses crypto/rand internally
```

This provides 122 bits of entropy. Even if an attacker can enumerate session IDs, the
probability of guessing a valid one is negligible.

The session ID is not a secret in itself — it is transmitted in HTTP headers and URL paths.
Its purpose is to associate a lock state with a specific proxy instance, not to authenticate.
Authentication (future work) is handled separately via the platform token.

---

## In-Memory Credential Storage

In the PoC, credentials are stored as plaintext Go structs in memory.

Risks:
- **Core dumps**: if the WCM process crashes and produces a core dump, credentials are
  exposed in the dump file
- **Memory scanning**: a privileged process (root or same UID) can read `/proc/<pid>/mem`

Mitigations (future work, outside PoC scope):
- Use `mlock` to prevent the credential memory pages from being swapped to disk
- Zero-fill credential strings when they are no longer needed
- Run WCM as a dedicated service user with a non-dumpable process flag (`prctl PR_SET_DUMPABLE 0`)
- Replace in-memory store with HashiCorp Vault (plugs in via the strategy pattern)

---

## Summary: PoC → Production Checklist

- [ ] Add platform-issued workspace token validation in WCM
- [ ] Inject proxy token via file (mode 0400) at workspace startup
- [ ] Enable TLS on WCM (or terminate at a trusted reverse proxy)
- [ ] Authenticate approval web page via platform reverse proxy
- [ ] Add user identity verification on approve/deny endpoints
- [ ] Run WCM as a dedicated service user
- [ ] Set `prctl PR_SET_DUMPABLE 0` on WCM startup
- [ ] Implement token rotation in the proxy (inotify on token file)
- [ ] Replace in-memory store with Vault or encrypted DB backend
- [ ] Add audit log (who accessed which credential, when, from which workspace)
