package api

import (
	"io/fs"
	"net/http"
	"strings"
	"wcm/internal/approval"
	"wcm/internal/lock"
	"wcm/internal/sse"
	"wcm/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(
	credStore store.CredentialStore,
	lockMgr *lock.Manager,
	approvalMgr *approval.Manager,
	broker *sse.Broker,
	uiFS fs.FS,
) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	ch := &credentialHandler{store: credStore, lockMgr: lockMgr}
	sh := &sessionHandler{lockMgr: lockMgr}
	ah := &approvalHandler{approvalMgr: approvalMgr, lockMgr: lockMgr, broker: broker}
	eh := &eventHandler{broker: broker}

	r.Route("/api/users/{userId}", func(r chi.Router) {
		r.Route("/credentials", func(r chi.Router) {
			r.Get("/", ch.search)
			r.Post("/", ch.create)
			r.Get("/{credentialId}", ch.get)
			r.Put("/{credentialId}", ch.update)
			r.Delete("/{credentialId}", ch.delete)
		})

		r.Route("/sessions", func(r chi.Router) {
			r.Get("/", sh.list)
			r.Post("/", sh.register)
			r.Delete("/{sessionId}", sh.unregister)
			r.Get("/{sessionId}/lock", sh.getLock)
			r.Post("/{sessionId}/lock", sh.forceLock)
			r.Post("/{sessionId}/unlock", ah.requestUnlock)
		})

		r.Get("/events", eh.stream)
	})

	// Approval resolution endpoints (approvalId is globally unique — no user scoping needed).
	r.Get("/api/approvals/{approvalId}", ah.getApproval)
	r.Post("/api/approvals/{approvalId}/approve", ah.approve)
	r.Post("/api/approvals/{approvalId}/deny", ah.deny)

	// Approval web page.
	r.Get("/approve/{approvalId}", ah.approvePage)

	// Web UI — served from the embedded static FS.
	// Serve index.html directly for root paths to avoid FileServer redirect loops.
	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(uiFS, "index.html")
		if err != nil {
			http.Error(w, "UI not built — run: make ui", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}
	fileServer := http.FileServerFS(uiFS)
	r.Get("/ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
	})
	r.Get("/ui/", serveIndex)
	r.Get("/ui/*", func(w http.ResponseWriter, r *http.Request) {
		// Strip /ui prefix so the file server maps /ui/bundle.js → /bundle.js
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/ui")
		fileServer.ServeHTTP(w, r2)
	})

	return r
}
