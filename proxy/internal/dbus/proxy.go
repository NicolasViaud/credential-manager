// Package dbus implements the org.freedesktop.secrets D-Bus service.
// It translates Secret Service API calls into WCM REST API calls.
package dbus

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"proxy/internal/client"
)

// D-Bus object paths.
const (
	servicePath    = "/org/freedesktop/secrets"
	collectionPath = "/org/freedesktop/secrets/collection/default"

	// aliasPath is the alias for the default collection.
	// Modern libsecret bypasses ReadAlias() and accesses this path directly;
	// without it, proxy creation fails with "Object does not implement the interface".
	aliasPath = "/org/freedesktop/secrets/aliases/default"
)

// D-Bus interface names.
const (
	serviceIface    = "org.freedesktop.Secret.Service"
	collectionIface = "org.freedesktop.Secret.Collection"
	itemIface       = "org.freedesktop.Secret.Item"
	sessionIface    = "org.freedesktop.Secret.Session"
	promptIface     = "org.freedesktop.Secret.Prompt"
	propsIface      = "org.freedesktop.DBus.Properties"
	introspectIface = "org.freedesktop.DBus.Introspectable"
)

// Secret is the D-Bus Secret struct — wire signature (oayays).
// Used as an OUT type only; godbus marshals Go structs by iterating fields.
// For IN parameters, use []any and parseSecret instead.
type Secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// parseSecret converts a D-Bus struct received as an IN parameter.
// godbus decodes incoming D-Bus structs as []any when they arrive as IN
// parameters, not as named Go structs. Methods that accept a secret must
// declare the parameter as []any and call this helper.
func parseSecret(raw []any) Secret {
	var s Secret
	if len(raw) > 0 {
		if v, ok := raw[0].(dbus.ObjectPath); ok {
			s.Session = v
		}
	}
	if len(raw) > 1 {
		if v, ok := raw[1].([]byte); ok {
			s.Parameters = v
		}
	}
	if len(raw) > 2 {
		if v, ok := raw[2].([]byte); ok {
			s.Value = v
		}
	}
	if len(raw) > 3 {
		if v, ok := raw[3].(string); ok {
			s.ContentType = v
		}
	}
	if s.ContentType == "" {
		s.ContentType = "text/plain; charset=utf8"
	}
	return s
}

// Proxy coordinates all D-Bus interfaces and holds shared state.
type Proxy struct {
	conn            *dbus.Conn
	wcm             *client.WCMClient
	approvalTimeout time.Duration
	pollInterval    time.Duration

	mu            sync.Mutex
	dbSessions    map[string]bool
	exportedItems map[string]bool

	sessionID atomic.Uint64
	promptID  atomic.Uint64
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

// mustExport panics on export failure — errors here are programming mistakes.
func (p *Proxy) mustExport(v any, path dbus.ObjectPath, iface string) {
	if err := p.conn.Export(v, path, iface); err != nil {
		panic(fmt.Sprintf("dbus export %s at %s: %v", iface, path, err))
	}
}

// Export registers all D-Bus objects on the connection and claims the well-known name.
func (p *Proxy) Export(busName string) error {
	// Service object.
	p.mustExport(&serviceHandler{p: p}, dbus.ObjectPath(servicePath), serviceIface)
	p.mustExport(&serviceProps{p: p}, dbus.ObjectPath(servicePath), propsIface)
	p.mustExport(introspect.Introspectable(serviceIntrospectXML), dbus.ObjectPath(servicePath), introspectIface)

	// Default collection — exported at BOTH the real path and the alias path.
	// Spec: "The default alias must resolve to the same collection as its real path."
	// Modern libsecret bypasses ReadAlias() and hits aliasPath directly.
	coll := &collectionHandler{p: p}
	collProps := &collectionProps{p: p}
	for _, path := range []dbus.ObjectPath{dbus.ObjectPath(collectionPath), dbus.ObjectPath(aliasPath)} {
		p.mustExport(coll, path, collectionIface)
		p.mustExport(collProps, path, propsIface)
		p.mustExport(introspect.Introspectable(collectionIntrospectXML), path, introspectIface)
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

// newPrompt creates and exports a Prompt object, returning its path.
// The Prompt runs the approval flow asynchronously and emits Completed when done.
func (p *Proxy) newPrompt(caller string, objects []dbus.ObjectPath) dbus.ObjectPath {
	id := p.promptID.Add(1)
	path := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/secrets/prompts/p%d", id))
	ph := &promptHandler{p: p, path: path, caller: caller, objects: objects}
	p.mustExport(ph, path, promptIface)
	p.mustExport(introspect.Introspectable(promptIntrospectXML), path, introspectIface)
	return path
}

// ensureUnlocked checks lock state and, if locked, triggers the WCM approval flow.
// Blocks until the session is unlocked or approvalTimeout elapses.
// Called by GetSecrets and Item.GetSecret — on the happy path these are invoked
// after a successful Unlock/Prompt flow and find the session already unlocked.
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
		if err := p.conn.Export(introspect.Introspectable(itemIntrospectXML), path, introspectIface); err != nil {
			log.Printf("[proxy] export item introspect %s: %v", cred.ID, err)
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
	p.conn.Export(nil, path, introspectIface)
	p.mu.Lock()
	delete(p.exportedItems, credID)
	p.mu.Unlock()
}

// itemPath returns the D-Bus object path for a credential ID.
// D-Bus path elements may only contain [A-Za-z0-9_], so UUID hyphens are stripped.
func itemPath(credID string) string {
	return collectionPath + "/" + strings.ReplaceAll(credID, "-", "")
}

// credIDFromPath extracts the credential ID from an item object path.
// If the path element looks like a stripped UUID (32 hex chars), hyphens are reinserted.
func credIDFromPath(p dbus.ObjectPath) string {
	s := string(p)
	idx := strings.LastIndex(s, "/")
	if idx < 0 {
		return s
	}
	raw := s[idx+1:]
	if len(raw) == 32 {
		return raw[0:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
	}
	return raw
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
