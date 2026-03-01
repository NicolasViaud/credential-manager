package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// Event is a single SSE message to be broadcast to subscribers.
type Event struct {
	// ID is the SSE event ID (used for Last-Event-ID reconnection).
	// Leave empty to omit the id field.
	ID   string
	// Type is the SSE event type (e.g. "approval_requested").
	Type string
	// Data is the payload — will be JSON-encoded into the data field.
	Data any
}

// Broker fans out events to all SSE subscribers of a given user.
type Broker struct {
	mu      sync.RWMutex
	clients map[string]map[chan Event]struct{} // userID → set of subscriber channels
}

func NewBroker() *Broker {
	return &Broker{
		clients: make(map[string]map[chan Event]struct{}),
	}
}

// Publish sends an event to all active subscribers for userID.
// Non-blocking: slow or disconnected subscribers are skipped.
func (b *Broker) Publish(userID string, event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.clients[userID] {
		select {
		case ch <- event:
		default: // subscriber channel is full — skip rather than block
		}
	}
}

// subscribe registers a new subscriber channel for userID.
func (b *Broker) subscribe(userID string) chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan Event, 16)
	if b.clients[userID] == nil {
		b.clients[userID] = make(map[chan Event]struct{})
	}
	b.clients[userID][ch] = struct{}{}
	return ch
}

// unsubscribe removes and closes the subscriber channel.
func (b *Broker) unsubscribe(userID string, ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.clients[userID], ch)
	close(ch)
}

// ServeHTTP streams SSE events to the client until the request context is cancelled.
// Call this directly from an http.HandlerFunc after resolving the userID.
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request, userID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // prevent nginx from buffering the stream

	ch := b.subscribe(userID)
	defer b.unsubscribe(userID, ch)

	// Send an initial comment so the client knows the connection is alive.
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}
			if event.ID != "" {
				fmt.Fprintf(w, "id: %s\n", event.ID)
			}
			fmt.Fprintf(w, "event: %s\n", event.Type)
			data, _ := json.Marshal(event.Data)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}
