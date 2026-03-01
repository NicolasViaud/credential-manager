# D-Bus Proxy Specification

## Overview

The D-Bus Credential Proxy is a Go daemon that registers itself on the D-Bus session bus as
the system's Secret Service provider. Any Linux application that uses `libsecret` (Git, SSH
agent, GNOME applications, `secret-tool`, etc.) will automatically route credential operations
through this proxy without requiring any application-side configuration.

The proxy translates D-Bus method calls into WCM REST API calls and handles the lock/approval
flow transparently — the calling application simply blocks until the user approves or denies.

---

## D-Bus Service Registration

The proxy registers the well-known D-Bus name:

```
org.freedesktop.secrets
```

And exposes objects implementing the following interfaces:

| Object Path | Interface |
|---|---|
| `/org/freedesktop/secrets` | `org.freedesktop.Secret.Service` |
| `/org/freedesktop/secrets/collection/{name}` | `org.freedesktop.Secret.Collection` |
| `/org/freedesktop/secrets/collection/{name}/{id}` | `org.freedesktop.Secret.Item` |
| `/org/freedesktop/secrets/session/{id}` | `org.freedesktop.Secret.Session` |

This matches the [Secret Service API specification](https://specifications.freedesktop.org/secret-service/).

---

## Implemented Interfaces

### org.freedesktop.Secret.Service

#### OpenSession

```
OpenSession(algorithm: String, input: Variant) → (output: Variant, result: ObjectPath)
```

Initiates an encryption session between the client and the service.

- Supported algorithm: `"plain"` (PoC; no encryption needed for local proxy)
- Returns a session object path: `/org/freedesktop/secrets/session/<uuid>`
- The session object is ephemeral and not persisted

Implementation note: In the PoC, only `plain` algorithm is supported. The `input` and `output`
Variants are both empty byte arrays. The returned object path is an in-memory handle.

#### SearchItems

```
SearchItems(attributes: Dict<String,String>) → (unlocked: Array<ObjectPath>, locked: Array<ObjectPath>)
```

Searches for items matching the given attribute map across all collections.

- Queries WCM `GET /api/users/{email}/credentials` with the provided attributes
- If the session is **unlocked**: returns all matching items in `unlocked`, `locked` is empty
- If the session is **locked**: returns all matching items in `locked`, `unlocked` is empty

The returned object paths have the form:
`/org/freedesktop/secrets/collection/default/<credentialId>`

#### Unlock

```
Unlock(objects: Array<ObjectPath>) → (unlocked: Array<ObjectPath>, prompt: ObjectPath)
```

Requests that the given objects (collections or items) be unlocked.

- For each locked collection or item, triggers the WCM unlock flow (POST /unlock)
- Blocks until the user approves or denies (polls WCM lock state every 500ms)
- On approval: returns the objects in `unlocked`, `prompt` is `"/"`
- On denial: returns empty `unlocked`, `prompt` is `"/"`
- `"/"` for `prompt` means no additional D-Bus prompt is needed (already handled)

#### Lock

```
Lock(objects: Array<ObjectPath>) → (locked: Array<ObjectPath>, prompt: ObjectPath)
```

Not commonly used, but implemented for completeness.

- Marks the proxy's WCM session as locked immediately
- Calls WCM `DELETE /api/users/{email}/sessions/{sessionId}` and re-registers
  (effectively resets the lock state)

#### GetSecrets

```
GetSecrets(items: Array<ObjectPath>, session: ObjectPath) → (secrets: Dict<ObjectPath, Secret>)
```

Retrieves secrets for the specified item paths.

This is the most critical method. Flow:

```
1. For each item path, extract credentialId from the path
2. Check lock state: GET /api/users/{email}/sessions/{sessionId}/lock
3. If locked:
   a. POST /api/users/{email}/sessions/{sessionId}/unlock
   b. Log the approvalUrl to stderr (fallback if no VSCode)
   c. Poll GET /lock every 500ms for up to WCM_APPROVAL_TIMEOUT (2 min)
   d. On approval: continue to step 4
   e. On denial/timeout: return D-Bus error org.freedesktop.Secret.Error.IsLocked
4. GET /api/users/{email}/credentials/{id} for each item
5. Return secrets map: ObjectPath → Secret{session, parameters, value, contentType}
```

The `Secret` struct:

```
Secret {
  session:     ObjectPath   (the session object path from OpenSession)
  parameters:  []byte       (empty for "plain" algorithm)
  value:       []byte       (UTF-8 encoded secret string)
  contentType: String       ("text/plain; charset=utf8")
}
```

#### CreateCollection

```
CreateCollection(properties: Dict<String,Variant>, alias: String) → (collection: ObjectPath, prompt: ObjectPath)
```

Creates a new named collection (keyring).

- In PoC: only `"default"` collection is supported
- Returns the collection object path
- `prompt` is `"/"` (no prompt needed)

#### ReadAlias

```
ReadAlias(name: String) → collection: ObjectPath
```

Returns the object path for a named alias (e.g., `"default"`).

- `"default"` → `/org/freedesktop/secrets/collection/default`
- Unknown alias → `"/"`

#### SetAlias

```
SetAlias(name: String, collection: ObjectPath) → nothing
```

In PoC: no-op (only `"default"` is supported).

---

### org.freedesktop.Secret.Collection

Object path: `/org/freedesktop/secrets/collection/{name}`

#### CreateItem

```
CreateItem(properties: Dict<String,Variant>, secret: Secret, replace: Boolean) → (item: ObjectPath, prompt: ObjectPath)
```

Creates a new credential in this collection.

- Extracts `org.freedesktop.Secret.Item.Label` and `org.freedesktop.Secret.Item.Attributes`
  from `properties`
- Calls WCM `POST /api/users/{email}/credentials`
- If `replace` is true and a matching item exists (same attributes), updates it instead
- Returns the new item's object path
- `prompt` is `"/"`

#### SearchItems

```
SearchItems(attributes: Dict<String,String>) → items: Array<ObjectPath>
```

Searches within this collection only. Delegates to WCM search filtered by `collection` name.

#### Delete

```
Delete() → prompt: ObjectPath
```

Deletes the collection and all its items.
In PoC: only `"default"` exists; this is a no-op that returns `"/"`.

**Properties (read-only):**

| Property | Type | Description |
|---|---|---|
| `Items` | `ao` | All item object paths in the collection |
| `Label` | `s` | Collection name |
| `Locked` | `b` | Whether the collection is locked (mirrors session lock state) |
| `Created` | `t` | Unix timestamp |
| `Modified` | `t` | Unix timestamp |

---

### org.freedesktop.Secret.Item

Object path: `/org/freedesktop/secrets/collection/{collection}/{credentialId}`

#### GetSecret

```
GetSecret(session: ObjectPath) → secret: Secret
```

Retrieves the secret for this item. Respects lock state (same flow as `GetSecrets`).

#### SetSecret

```
SetSecret(session: ObjectPath, secret: Secret) → nothing
```

Updates the secret value. Calls WCM `PUT /api/users/{email}/credentials/{id}`.

#### Delete

```
Delete() → prompt: ObjectPath
```

Deletes this item. Calls WCM `DELETE /api/users/{email}/credentials/{id}`.
Returns `"/"`.

**Properties (read/write):**

| Property | Type | R/W | Description |
|---|---|---|---|
| `Locked` | `b` | R | Mirrors session lock state |
| `Attributes` | `a{ss}` | R/W | Key-value attribute map |
| `Label` | `s` | R/W | Human-readable label |
| `Type` | `s` | R/W | Item type (optional; empty string in PoC) |
| `Created` | `t` | R | Unix timestamp |
| `Modified` | `t` | R | Unix timestamp |

---

### org.freedesktop.Secret.Session

Object path: `/org/freedesktop/secrets/session/{id}`

#### Close

```
Close() → nothing
```

Closes the encryption session. In PoC: no-op (plain algorithm has no state to clean up).

---

## D-Bus Signals

| Interface | Signal | Description |
|---|---|---|
| `org.freedesktop.Secret.Service` | `CollectionCreated(collection: o)` | Emitted when a collection is created |
| `org.freedesktop.Secret.Service` | `CollectionDeleted(collection: o)` | Emitted when a collection is deleted |
| `org.freedesktop.Secret.Service` | `CollectionChanged(collection: o)` | Emitted when a collection changes |
| `org.freedesktop.Secret.Collection` | `ItemCreated(item: o)` | Emitted when an item is created |
| `org.freedesktop.Secret.Collection` | `ItemDeleted(item: o)` | Emitted when an item is deleted |
| `org.freedesktop.Secret.Collection` | `ItemChanged(item: o)` | Emitted when an item changes |

In PoC: signals are emitted as best-effort; no guarantee of delivery order.

---

## Lock Polling Strategy

When the proxy needs to wait for approval, it polls the WCM lock endpoint:

```
interval:  500ms
max_wait:  WCM_APPROVAL_TIMEOUT (default: 2 minutes)
backoff:   none (fixed interval for simplicity)
```

While polling, the D-Bus call is blocked. `libsecret` and `secret-tool` handle this
transparently — they wait for the D-Bus reply with no timeout of their own by default.

---

## Compatibility Test: secret-tool

The proxy must pass the following `secret-tool` operations end-to-end:

```bash
# Store a credential
secret-tool store --label="Test password" service myservice account myaccount

# Retrieve a credential (triggers approval flow if locked)
secret-tool lookup service myservice account myaccount

# List credentials
secret-tool search service myservice

# Delete a credential
secret-tool clear service myservice account myaccount
```

These four operations exercise all critical code paths in the proxy.

---

## Proxy Configuration

| Env Var | Required | Default | Description |
|---|---|---|---|
| `WCM_BASE_URL` | yes | — | Base URL of the WCM service (e.g., `http://localhost:8080`) |
| `OWNER_EMAIL` | yes | — | Injected by CDE platform; used as `userId` for all WCM calls |
| `WCM_POLL_INTERVAL` | no | `500ms` | Polling interval while waiting for approval |
| `WCM_DBUS_NAME` | no | `org.freedesktop.secrets` | D-Bus service name to register |

---

## Error Handling

| Situation | D-Bus Error Returned |
|---|---|
| User denies approval | `org.freedesktop.Secret.Error.IsLocked` |
| Approval timeout | `org.freedesktop.Secret.Error.IsLocked` |
| WCM unreachable | `org.freedesktop.DBus.Error.Failed` with message |
| Credential not found | `org.freedesktop.Secret.Error.NoSuchObject` |
| Internal proxy error | `org.freedesktop.DBus.Error.Failed` |
