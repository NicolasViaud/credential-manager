package dbus

import (
	"context"
	"log"

	"github.com/godbus/dbus/v5"
)

// promptHandler implements org.freedesktop.Secret.Prompt.
//
// The Secret Service spec uses Prompts for operations that require user
// interaction. Prompt() returns void immediately and fires a Completed
// signal once the user approves or denies. This avoids the ~25s D-Bus
// method-call timeout that would fire if we blocked inside Unlock.
type promptHandler struct {
	p       *Proxy
	path    dbus.ObjectPath
	caller  string
	objects []dbus.ObjectPath // item/collection paths to report as unlocked on success
}

// Prompt opens the WCM approval flow in a background goroutine and returns
// immediately. The Completed signal is emitted once the user acts.
//
// D-Bus: Prompt(s window-id)
func (h *promptHandler) Prompt(_ string) *dbus.Error {
	log.Printf("[proxy] Prompt.Prompt caller=%s objects=%v", h.caller, h.objects)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), h.p.approvalTimeout)
		defer cancel()

		resp, err := h.p.wcm.RequestUnlock(ctx, h.caller)
		if err != nil {
			log.Printf("[proxy] Prompt: RequestUnlock failed: %v", err)
			h.emitCompleted(true, nil)
			return
		}
		if resp != nil {
			log.Printf("[proxy] Prompt: approval required — %s", resp.ApprovalURL)
		}

		if err := h.p.wcm.PollUntilUnlocked(ctx, h.p.pollInterval); err != nil {
			log.Printf("[proxy] Prompt: approval timed out or denied: %v", err)
			h.emitCompleted(true, nil)
			return
		}

		log.Printf("[proxy] Prompt: unlocked, signalling Completed")
		h.emitCompleted(false, h.objects)
	}()
	return nil
}

// Dismiss cancels the prompt without granting access.
//
// D-Bus: Dismiss()
func (h *promptHandler) Dismiss() *dbus.Error {
	log.Printf("[proxy] Prompt.Dismiss path=%s", h.path)
	h.emitCompleted(true, nil)
	return nil
}

// emitCompleted fires the Completed signal and cleans up the prompt object.
func (h *promptHandler) emitCompleted(dismissed bool, unlocked []dbus.ObjectPath) {
	if unlocked == nil {
		unlocked = []dbus.ObjectPath{}
	}
	h.p.conn.Emit(h.path, "org.freedesktop.Secret.Prompt.Completed",
		dismissed, dbus.MakeVariant(unlocked))
	h.p.conn.Export(nil, h.path, promptIface)
	h.p.conn.Export(nil, h.path, introspectIface)
}
