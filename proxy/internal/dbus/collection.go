package dbus

import (
	"context"
	"log"
	"time"

	"github.com/godbus/dbus/v5"
	"proxy/internal/client"
)

// collectionHandler implements org.freedesktop.Secret.Collection on the default collection.
type collectionHandler struct{ p *Proxy }

// CreateItem creates a new credential in the default collection.
// When replace is true and an item with identical attributes already exists, it is updated.
func (h *collectionHandler) CreateItem(
	properties map[string]dbus.Variant,
	secret Secret,
	replace bool,
) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	ctx := context.Background()

	// Extract label and attributes from the D-Bus properties dict.
	label := ""
	if v, ok := properties["org.freedesktop.Secret.Item.Label"]; ok {
		if s, ok := v.Value().(string); ok {
			label = s
		}
	}
	attrs := map[string]string{}
	if v, ok := properties["org.freedesktop.Secret.Item.Attributes"]; ok {
		if m, ok := v.Value().(map[string]string); ok {
			attrs = m
		}
	}
	if label == "" {
		label = "Unnamed credential"
	}
	secretStr := string(secret.Value)

	// If replace=true, look for an existing item with matching attributes and update it.
	if replace && len(attrs) > 0 {
		existing, err := h.p.wcm.SearchCredentials(ctx, "default", attrs)
		if err == nil && len(existing) > 0 {
			cred, err := h.p.wcm.UpdateCredential(ctx, existing[0].ID, label, secretStr, attrs)
			if err == nil {
				h.p.ensureItemsExported([]*client.Credential{cred})
				log.Printf("[proxy] CreateItem: updated existing %s", cred.ID)
				return dbus.ObjectPath(itemPath(cred.ID)), "/", nil
			}
			log.Printf("[proxy] CreateItem: update failed, creating new: %v", err)
		}
	}

	cred, err := h.p.wcm.CreateCredential(ctx, label, secretStr, "default", attrs)
	if err != nil {
		return "/", "/", dbusError(err.Error())
	}
	h.p.ensureItemsExported([]*client.Credential{cred})
	log.Printf("[proxy] CreateItem: created %s", cred.ID)
	return dbus.ObjectPath(itemPath(cred.ID)), "/", nil
}

// SearchItems returns all item paths in this collection matching the given attributes.
func (h *collectionHandler) SearchItems(attrs map[string]string) ([]dbus.ObjectPath, *dbus.Error) {
	ctx := context.Background()
	creds, err := h.p.wcm.SearchCredentials(ctx, "default", attrs)
	if err != nil {
		return nil, dbusError(err.Error())
	}
	h.p.ensureItemsExported(creds)

	paths := make([]dbus.ObjectPath, len(creds))
	for i, c := range creds {
		paths[i] = dbus.ObjectPath(itemPath(c.ID))
	}
	return paths, nil
}

// Delete is a no-op for the default collection (only one collection exists in the PoC).
func (h *collectionHandler) Delete() (dbus.ObjectPath, *dbus.Error) {
	return "/", nil
}

// collectionProps implements org.freedesktop.DBus.Properties for the collection object.
type collectionProps struct{ p *Proxy }

func (p *collectionProps) Get(iface, name string) (dbus.Variant, *dbus.Error) {
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

func (p *collectionProps) GetAll(_ string) (map[string]dbus.Variant, *dbus.Error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	creds, _ := p.p.wcm.SearchCredentials(ctx, "default", nil)
	p.p.ensureItemsExported(creds)

	items := make([]dbus.ObjectPath, len(creds))
	for i, c := range creds {
		items[i] = dbus.ObjectPath(itemPath(c.ID))
	}

	locked, _ := p.p.wcm.CheckLock(ctx)

	return map[string]dbus.Variant{
		"Items":    dbus.MakeVariant(items),
		"Label":    dbus.MakeVariant("default"),
		"Locked":   dbus.MakeVariant(locked),
		"Created":  dbus.MakeVariant(uint64(0)),
		"Modified": dbus.MakeVariant(uint64(0)),
	}, nil
}

func (p *collectionProps) Set(_, _ string, _ dbus.Variant) *dbus.Error {
	return &dbus.Error{Name: "org.freedesktop.DBus.Error.PropertyReadOnly"}
}
