package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"
	"wcm/internal/api"
	"wcm/internal/approval"
	"wcm/internal/lock"
	"wcm/internal/sse"
	"wcm/internal/store"
)

//go:embed static
var staticFiles embed.FS

func main() {
	port            := getEnv("WCM_PORT", "8080")
	baseURL         := getEnv("WCM_BASE_URL", "http://localhost:"+port)
	lockTimeout     := getDuration("WCM_LOCK_TIMEOUT", 10*time.Minute)
	approvalTimeout := getDuration("WCM_APPROVAL_TIMEOUT", 2*time.Minute)

	credStore   := store.NewMemoryStore()
	lockMgr     := lock.NewManager(lockTimeout)
	approvalMgr := approval.NewManager(approvalTimeout, baseURL)
	broker      := sse.NewBroker()

	// Background goroutine: expire stale approval requests and broadcast SSE events.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			for _, req := range approvalMgr.ExpireOld() {
				broker.Publish(req.UserID, sse.Event{
					ID:   req.ID,
					Type: "approval_resolved",
					Data: map[string]any{
						"type":       "approval_resolved",
						"approvalId": req.ID,
						"status":     "expired",
					},
				})
			}
		}
	}()

	uiFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}

	router := api.NewRouter(credStore, lockMgr, approvalMgr, broker, uiFS)

	log.Printf("WCM listening on :%s  (base URL: %s)", port, baseURL)
	log.Printf("UI available at  %s/ui", baseURL)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatal(err)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("warning: invalid duration for %s, using default %s", key, fallback)
	}
	return fallback
}

