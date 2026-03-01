package dbus

import (
	"log"

	"github.com/godbus/dbus/v5"
)

// sessionHandler implements org.freedesktop.Secret.Session.
// Each D-Bus client gets its own session object via OpenSession.
type sessionHandler struct {
	p  *Proxy
	id string
}

// Close terminates this encryption session.
// In the PoC (plain algorithm), there is no cryptographic state to clean up.
func (h *sessionHandler) Close() *dbus.Error {
	h.p.mu.Lock()
	delete(h.p.dbSessions, h.id)
	h.p.mu.Unlock()
	log.Printf("[proxy] D-Bus session closed: %s", h.id)
	return nil
}
