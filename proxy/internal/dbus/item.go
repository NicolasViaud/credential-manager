package dbus

import (
	"context"
	"log"
	"time"

	"github.com/godbus/dbus/v5"
)

// itemHandler implements org.freedesktop.Secret.Item for a single credential.
type itemHandler struct {
	p      *Proxy
	credID string
}

// GetSecret retrieves the secret for this item.
// Triggers the unlock flow if the session is locked.
func (h *itemHandler) GetSecret(session dbus.ObjectPath) (Secret, *dbus.Error) {
	if dbErr := h.p.ensureUnlocked("dbus-proxy"); dbErr != nil {
		return Secret{}, dbErr
	}
	ctx := context.Background()
	cred, err := h.p.wcm.GetCredential(ctx, h.credID)
	if err != nil {
		if err.Error() == "not_found" {
			return Secret{}, noSuchObject()
		}
		return Secret{}, dbusError(err.Error())
	}
	return Secret{
		Session:     session,
		Parameters:  []byte{},
		Value:       []byte(cred.Secret),
		ContentType: "text/plain; charset=utf8",
	}, nil
}

// SetSecret updates this item's secret value (label and attributes are preserved).
// rawSecret is []any — same reason as CreateItem (godbus decodes D-Bus struct IN params as []any).
func (h *itemHandler) SetSecret(rawSecret []any) *dbus.Error {
	secret := parseSecret(rawSecret)
	ctx := context.Background()
	// Fetch current credential to preserve label and attributes.
	cred, err := h.p.wcm.GetCredentialNoLock(ctx, h.credID)
	if err != nil {
		if err.Error() == "not_found" {
			return noSuchObject()
		}
		return dbusError(err.Error())
	}
	_, err = h.p.wcm.UpdateCredential(ctx, h.credID, cred.Label, string(secret.Value), cred.Attributes)
	if err != nil {
		return dbusError(err.Error())
	}
	return nil
}

// Delete removes this credential from WCM and un-exports the D-Bus object.
func (h *itemHandler) Delete() (dbus.ObjectPath, *dbus.Error) {
	ctx := context.Background()
	if err := h.p.wcm.DeleteCredential(ctx, h.credID); err != nil {
		return "/", dbusError(err.Error())
	}
	h.p.removeExportedItem(h.credID)
	log.Printf("[proxy] deleted item: %s", h.credID)
	return "/", nil
}

// itemProps implements org.freedesktop.DBus.Properties for item objects.
type itemProps struct {
	p      *Proxy
	credID string
}

func (p *itemProps) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	all, err := p.GetAll(iface)
	if err != nil {
		return dbus.Variant{}, err
	}
	v, ok := all[name]
	if !ok {
		return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownProperty"}
	}
	return v, nil
}

func (p *itemProps) GetAll(_ string) (map[string]dbus.Variant, *dbus.Error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use no-lock variant so metadata is readable even when the session is locked.
	cred, err := p.p.wcm.GetCredentialNoLock(ctx, p.credID)
	if err != nil {
		return nil, noSuchObject()
	}

	locked, _ := p.p.wcm.CheckLock(ctx)

	attrs := cred.Attributes
	if attrs == nil {
		attrs = map[string]string{}
	}

	return map[string]dbus.Variant{
		"Locked":     dbus.MakeVariant(locked),
		"Attributes": dbus.MakeVariant(attrs),
		"Label":      dbus.MakeVariant(cred.Label),
		"Type":       dbus.MakeVariant(""),
		"Created":    dbus.MakeVariant(uint64(cred.CreatedAt.Unix())),
		"Modified":   dbus.MakeVariant(uint64(cred.UpdatedAt.Unix())),
	}, nil
}

// Set supports updating Label and Attributes properties.
func (p *itemProps) Set(_ string, name string, val dbus.Variant) *dbus.Error {
	ctx := context.Background()
	cred, err := p.p.wcm.GetCredentialNoLock(ctx, p.credID)
	if err != nil {
		return noSuchObject()
	}
	switch name {
	case "Label":
		if s, ok := val.Value().(string); ok {
			_, err = p.p.wcm.UpdateCredential(ctx, p.credID, s, cred.Secret, cred.Attributes)
		}
	case "Attributes":
		if m, ok := val.Value().(map[string]string); ok {
			_, err = p.p.wcm.UpdateCredential(ctx, p.credID, cred.Label, cred.Secret, m)
		}
	default:
		return &dbus.Error{Name: "org.freedesktop.DBus.Error.PropertyReadOnly"}
	}
	if err != nil {
		return dbusError(err.Error())
	}
	return nil
}
