package dbus

import (
	"context"
	"log"

	"github.com/godbus/dbus/v5"
	"github.com/google/uuid"
)

// serviceHandler implements org.freedesktop.Secret.Service.
type serviceHandler struct{ p *Proxy }

// OpenSession initiates a D-Bus encryption session.
// Only the "plain" algorithm is supported in the PoC.
func (h *serviceHandler) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	if algorithm != "plain" {
		return dbus.MakeVariant(""), "/", &dbus.Error{
			Name: "org.freedesktop.Secret.Error.NotSupported",
			Body: []interface{}{"only 'plain' algorithm is supported"},
		}
	}

	sessID := uuid.New().String()
	sessPath := dbus.ObjectPath("/org/freedesktop/secrets/session/" + sessID)

	sh := &sessionHandler{p: h.p, id: sessID}
	if err := h.p.conn.Export(sh, sessPath, sessionIface); err != nil {
		log.Printf("[proxy] export session %s: %v", sessPath, err)
	}

	h.p.mu.Lock()
	h.p.dbSessions[sessID] = true
	h.p.mu.Unlock()

	log.Printf("[proxy] OpenSession → %s", sessPath)
	// Return an empty-byte variant (no output key material for "plain") and the session path.
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

// Unlock triggers the WCM approval flow for the given objects.
// Blocks until the user approves or the approval timeout expires.
func (h *serviceHandler) Unlock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	if dbErr := h.p.ensureUnlocked("dbus-proxy"); dbErr != nil {
		return []dbus.ObjectPath{}, "/", dbErr
	}
	return objects, "/", nil
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
