package dbus

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

// serviceHandler implements org.freedesktop.Secret.Service.
type serviceHandler struct{ p *Proxy }

// OpenSession initiates a D-Bus encryption session.
// Only the "plain" algorithm is supported in the PoC.
func (h *serviceHandler) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	if algorithm != "plain" {
		return dbus.MakeVariant(""), "/", &dbus.Error{
			Name: "org.freedesktop.DBus.Error.NotSupported",
			Body: []interface{}{"only 'plain' algorithm is supported"},
		}
	}

	// D-Bus object path elements may only contain [A-Za-z0-9_].
	// UUIDs contain hyphens so we use a simple counter instead.
	id := h.p.sessionID.Add(1)
	sessPath := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/secrets/session/s%d", id))
	sessKey := fmt.Sprintf("s%d", id)

	sh := &sessionHandler{p: h.p, id: sessKey}
	if err := h.p.conn.Export(sh, sessPath, sessionIface); err != nil {
		log.Printf("[proxy] export session %s: %v", sessPath, err)
	}
	if err := h.p.conn.Export(introspect.Introspectable(sessionIntrospectXML), sessPath, introspectIface); err != nil {
		log.Printf("[proxy] export session introspect %s: %v", sessPath, err)
	}

	h.p.mu.Lock()
	h.p.dbSessions[sessKey] = true
	h.p.mu.Unlock()

	log.Printf("[proxy] OpenSession → %s", sessPath)
	return dbus.MakeVariant([]byte{}), sessPath, nil
}

// SearchItems searches for credentials matching the given attributes across all collections.
// Returns (unlocked, locked) — split based on whether the WCM session is currently locked.
func (h *serviceHandler) SearchItems(attrs map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	ctx := context.Background()

	creds, err := h.p.wcm.SearchCredentials(ctx, "default", attrs)
	if err != nil {
		return nil, nil, dbusError(err.Error())
	}
	h.p.ensureItemsExported(creds)

	paths := make([]dbus.ObjectPath, len(creds))
	for i, c := range creds {
		paths[i] = dbus.ObjectPath(itemPath(c.ID))
	}

	locked, err := h.p.wcm.CheckLock(ctx)
	if err != nil {
		return nil, nil, dbusError(err.Error())
	}

	if locked {
		return []dbus.ObjectPath{}, paths, nil
	}
	return paths, []dbus.ObjectPath{}, nil
}

// Unlock starts the WCM approval flow for the given objects.
// Returns a Prompt path immediately; the Prompt emits Completed when the user
// approves or denies. This avoids the ~25 s D-Bus method-call timeout that
// would fire if we blocked here waiting for user interaction.
//
// D-Bus: Unlock(ao objects) → (ao unlocked, o prompt)
func (h *serviceHandler) Unlock(sender dbus.Sender, objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	// Filter to objects that are actually lockable (collection or known items).
	var locked []dbus.ObjectPath
	for _, obj := range objects {
		if string(obj) == collectionPath || string(obj) == aliasPath {
			locked = append(locked, obj)
			continue
		}
		// Include item paths that belong to our collection.
		if strings.HasPrefix(string(obj), collectionPath+"/") {
			locked = append(locked, obj)
		}
	}

	if len(locked) == 0 {
		return objects, "/", nil
	}

	promptPath := h.p.newPrompt(string(sender), locked)
	return []dbus.ObjectPath{}, promptPath, nil
}

// Lock immediately locks the WCM session.
func (h *serviceHandler) Lock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	ctx := context.Background()
	if err := h.p.wcm.ForceLock(ctx); err != nil {
		return []dbus.ObjectPath{}, "/", dbusError(err.Error())
	}
	return objects, "/", nil
}

// GetSecrets retrieves secrets for the specified item paths.
// Triggers the unlock flow if the session is locked (blocks until approved).
func (h *serviceHandler) GetSecrets(items []dbus.ObjectPath, session dbus.ObjectPath) (map[dbus.ObjectPath]Secret, *dbus.Error) {
	if dbErr := h.p.ensureUnlocked("dbus-proxy"); dbErr != nil {
		return nil, dbErr
	}

	ctx := context.Background()
	result := make(map[dbus.ObjectPath]Secret, len(items))
	for _, path := range items {
		credID := credIDFromPath(path)
		cred, err := h.p.wcm.GetCredential(ctx, credID)
		if err != nil {
			log.Printf("[proxy] GetCredential %s: %v", credID, err)
			continue // skip items that are not found or otherwise fail
		}
		result[path] = Secret{
			Session:     session,
			Parameters:  []byte{},
			Value:       []byte(cred.Secret),
			ContentType: "text/plain; charset=utf8",
		}
	}
	return result, nil
}

// ReadAlias returns the collection path for a named alias.
func (h *serviceHandler) ReadAlias(name string) (dbus.ObjectPath, *dbus.Error) {
	if name == "default" || name == "login" {
		return dbus.ObjectPath(collectionPath), nil
	}
	return "/", nil
}

// SetAlias is a no-op in the PoC (only "default" is supported).
func (h *serviceHandler) SetAlias(name string, collection dbus.ObjectPath) *dbus.Error {
	return nil
}

// CreateCollection returns the existing default collection (only one is supported).
func (h *serviceHandler) CreateCollection(properties map[string]dbus.Variant, alias string) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	return dbus.ObjectPath(collectionPath), "/", nil
}

// serviceProps implements org.freedesktop.DBus.Properties for the service object.
type serviceProps struct{ p *Proxy }

func (p *serviceProps) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if name == "Collections" {
		return dbus.MakeVariant([]dbus.ObjectPath{dbus.ObjectPath(collectionPath)}), nil
	}
	return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownProperty"}
}

func (p *serviceProps) GetAll(_ string) (map[string]dbus.Variant, *dbus.Error) {
	return map[string]dbus.Variant{
		"Collections": dbus.MakeVariant([]dbus.ObjectPath{dbus.ObjectPath(collectionPath)}),
	}, nil
}

func (p *serviceProps) Set(_, _ string, _ dbus.Variant) *dbus.Error {
	return &dbus.Error{Name: "org.freedesktop.DBus.Error.PropertyReadOnly"}
}
