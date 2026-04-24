package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/jpillora/meads/pkg/meads"
)

// event is a change notification published to SSE subscribers.
type event struct {
	Kind   string      `json:"kind"`
	Task   *meads.Task `json:"task,omitempty"`
	TaskID int         `json:"task_id,omitempty"`
}

// eventBus fans out events to SSE subscribers.
type eventBus struct {
	mu      sync.Mutex
	clients map[chan event]struct{}
}

func newEventBus() *eventBus {
	return &eventBus{clients: make(map[chan event]struct{})}
}

func (b *eventBus) subscribe() chan event {
	ch := make(chan event, 16)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *eventBus) unsubscribe(ch chan event) {
	b.mu.Lock()
	if _, ok := b.clients[ch]; ok {
		delete(b.clients, ch)
		close(ch)
	}
	b.mu.Unlock()
}

func (b *eventBus) publish(e event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- e:
		default:
			// Slow subscriber — drop. They can reconnect.
		}
	}
}

func (b *eventBus) closeAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		close(ch)
	}
	b.clients = make(map[chan event]struct{})
}

// handleEvents serves Server-Sent Events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)

	// Initial comment to establish the stream.
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(e)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Kind, data)
			flusher.Flush()
		}
	}
}
