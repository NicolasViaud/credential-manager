// Package main is the D-Bus Secret Service proxy for the Web Credential Manager.
//
// It registers itself as "org.freedesktop.secrets" on the D-Bus session bus and
// translates Secret Service API calls (used by libsecret, secret-tool, Git, etc.)
// into WCM REST API calls.
//
// Required environment variables:
//
//	WCM_BASE_URL  — base URL of the WCM service, e.g. "http://localhost:8080"
//	OWNER_EMAIL   — workspace owner email; used as the WCM user ID
//
// Optional environment variables:
//
//	WCM_POLL_INTERVAL  — lock polling interval (default: 500ms)
//	WCM_APPROVAL_TIMEOUT — max wait for user approval (default: 2m)
//	WCM_DBUS_NAME      — D-Bus service name (default: "org.freedesktop.secrets")
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/google/uuid"
	"proxy/internal/client"
	dbussvc "proxy/internal/dbus"
)

func main() {
	// ── Configuration ────────────────────────────────────────────────────────

	wcmBaseURL := requireEnv("WCM_BASE_URL")
	ownerEmail := requireEnv("OWNER_EMAIL")

	busName := envOr("WCM_DBUS_NAME", "org.freedesktop.secrets")

	pollInterval, err := time.ParseDuration(envOr("WCM_POLL_INTERVAL", "500ms"))
	if err != nil {
		log.Fatalf("invalid WCM_POLL_INTERVAL: %v", err)
	}
	approvalTimeout, err := time.ParseDuration(envOr("WCM_APPROVAL_TIMEOUT", "2m"))
	if err != nil {
		log.Fatalf("invalid WCM_APPROVAL_TIMEOUT: %v", err)
	}

	// ── D-Bus connection ─────────────────────────────────────────────────────

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Fatalf("connect to D-Bus session bus: %v", err)
	}
	defer conn.Close()

	// ── WCM session registration ─────────────────────────────────────────────

	sessionID := uuid.New().String()
	workspaceID, _ := os.Hostname()

	wcm := client.New(wcmBaseURL, ownerEmail, sessionID)

	ctx := context.Background()
	if err := wcm.RegisterSession(ctx, workspaceID); err != nil {
		log.Fatalf("register WCM session: %v\n  Is WCM running at %s?", err, wcmBaseURL)
	}
	log.Printf("[proxy] registered WCM session %s for %s", sessionID, ownerEmail)

	// ── Export D-Bus service ─────────────────────────────────────────────────

	proxy := dbussvc.New(conn, wcm, approvalTimeout, pollInterval)
	if err := proxy.Export(busName); err != nil {
		log.Fatalf("export D-Bus service: %v", err)
	}

	log.Printf("[proxy] ready — listening on D-Bus as %s", busName)

	// ── Wait for termination ─────────────────────────────────────────────────

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[proxy] shutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wcm.UnregisterSession(shutdownCtx); err != nil {
		log.Printf("[proxy] unregister session: %v", err)
	}
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
