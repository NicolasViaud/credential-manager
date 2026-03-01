// Package dbus implements the org.freedesktop.secrets D-Bus service.
// It translates Secret Service API calls into WCM REST API calls.
package dbus

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"proxy/internal/client"
)

// D-Bus object paths.
const (
	servicePath    = "/org/freedesktop/secrets"
	collectionPath = "/org/freedesktop/secrets/collection/default"
)

// D-Bus interface names.
const (
	serviceIface    = "org.freedesktop.Secret.Service"
	collectionIface = "org.freedesktop.Secret.Collection"
	itemIface       = "org.freedesktop.Secret.Item"
	sessionIface    = "org.freedesktop.Secret.Session"
	propsIface      = "org.freedesktop.DBus.Properties"
)

// Secret is the D-Bus Secret struct — signature (oayays).
// It represents an encrypted (or plain) secret value exchanged over D-Bus.
type Secret struct {
	Session     dbus.ObjectPath // Encryption session object path
	Parameters  []byte          // Encryption parameters (empty for "plain")
	Value       []byte          // The secret bytes
	ContentType string          // MIME type, e.g. "text/plain; charset=utf8"
}

// Proxy coordinates all D-Bus interfaces and holds shared state.
type Proxy struct {
	conn            *dbus.Conn
	wcm             *client.WCMClient
	approvalTimeout time.Duration
	pollInterval    time.Duration

	mu            sync.Mutex
	dbSessions    map[string]bool // D-Bus session UUIDs (created via OpenSession)
	exportedItems map[string]bool // credential IDs already exported as D-Bus item objects
}

// New creates a Proxy ready to Export.
func New(conn *dbus.Conn, wcm *client.WCMClient, approvalTimeout, pollInterval time.Duration) *Proxy {
	return &Proxy{
		conn:            conn,
		wcm:             wcm,
		approvalTimeout: approvalTimeout,
		pollInterval:    pollInterval,
		dbSessions:      make(map[string]bool),
		exportedItems:   make(map[string]bool),
	}
}

// Export registers all D-Bus objects on the connection and returns the registered name.
func (p *Proxy) Export(busName string) error {
	exports := []struct {
		obj  any
		path dbus.ObjectPath
		face string
	}{
		{&serviceHandler{p: p}, dbus.ObjectPath(servicePath), serviceIface},
		{&serviceProps{p: p}, dbus.ObjectPath(servicePath), propsIface},
		{&collectionHandler{p: p}, dbus.ObjectPath(collectionPath), collectionIface},
		{&collectionProps{p: p}, dbus.ObjectPath(collectionPath), propsIface},
	}
	for _, e := range exports {
		if err := p.conn.Export(e.obj, e.path, e.face); err != nil {
			return fmt.Errorf("export %s at %s: %w", e.face, e.path, err)
		}
	}

	reply, err := p.conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("request name %q: %w", busName, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("D-Bus name %q already taken (reply=%d); is another secret service running?", busName, reply)
	}
	log.Printf("[proxy] registered as %s", busName)
	return nil
}

// ensureUnlocked checks lock state and, if locked, triggers the WCM approval flow.
// Blocks until the session is unlocked or approvalTimeout elapses.
func (p *Proxy) ensureUnlocked(appName string) *dbus.Error {
	ctx := context.Background()

	locked, err := p.wcm.CheckLock(ctx)
	if err != nil {
		return dbusError("lock check failed: " + err.Error())
	}
	if !locked {
		return nil
	}

	resp, err := p.wcm.RequestUnlock(ctx, appName)
	if err != nil {
		return dbusError("unlock request failed: " + err.Error())
	}
	if resp != nil {
		log.Printf("[proxy] approval required — open: %s", resp.ApprovalURL)
	}

	pollCtx, cancel := context.WithTimeout(ctx, p.approvalTimeout)
	defer cancel()

	if err := p.wcm.PollUntilUnlocked(pollCtx, p.pollInterval); err != nil {
		return &dbus.Error{
			Name: "org.freedesktop.Secret.Error.IsLocked",
			Body: []interface{}{"access denied or approval timed out"},
		}
	}
	return nil
}

// ensureItemsExported dynamically exports a D-Bus item object for each credential
// that hasn't been exported yet.
func (p *Proxy) ensureItemsExported(creds []*client.Credential) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, cred := range creds {
		if p.exportedItems[cred.ID] {
			continue
		}
		path := dbus.ObjectPath(itemPath(cred.ID))
		ih := &itemHandler{p: p, credID: cred.ID}
		if err := p.conn.Export(ih, path, itemIface); err != nil {
			log.Printf("[proxy] export item %s: %v", cred.ID, err)
			continue
		}
		ip := &itemProps{p: p, credID: cred.ID}
		if err := p.conn.Export(ip, path, propsIface); err != nil {
			log.Printf("[proxy] export item props %s: %v", cred.ID, err)
		}
		p.exportedItems[cred.ID] = true
		log.Printf("[proxy] exported item: %s", path)
	}
}

// removeExportedItem un-exports a D-Bus item object after deletion.
func (p *Proxy) removeExportedItem(credID string) {
	path := dbus.ObjectPath(itemPath(credID))
	p.conn.Export(nil, path, itemIface)
	p.conn.Export(nil, path, propsIface)
	p.mu.Lock()
	delete(p.exportedItems, credID)
	p.mu.Unlock()
}

// itemPath returns the D-Bus object path for a credential ID.
func itemPath(credID string) string {
	return collectionPath + "/" + credID
}

// credIDFromPath extracts the credential ID from an item object path.
func credIDFromPath(p dbus.ObjectPath) string {
	s := string(p)
	idx := strings.LastIndex(s, "/")
	if idx < 0 {
		return s
	}
	return s[idx+1:]
}

// dbusError creates a generic D-Bus error.
func dbusError(msg string) *dbus.Error {
	return &dbus.Error{
		Name: "org.freedesktop.DBus.Error.Failed",
		Body: []interface{}{msg},
	}
}

// noSuchObject returns a standard "no such object" D-Bus error.
func noSuchObject() *dbus.Error {
	return &dbus.Error{Name: "org.freedesktop.Secret.Error.NoSuchObject"}
}
