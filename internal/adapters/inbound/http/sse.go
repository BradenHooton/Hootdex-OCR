// Package httpapi is the inbound REST + Server-Sent Events adapter.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

const (
	sseBuffer    = 64
	sseHeartbeat = 15 * time.Second
)

// Broker fans pipeline events out to SSE subscribers. It implements ports.EventPublisher;
// Publish never blocks — a slow client drops events rather than stalling OCR workers.
type Broker struct {
	mu        sync.RWMutex
	subs      map[chan domain.PipelineEvent]struct{}
	seq       atomic.Int64
	heartbeat time.Duration
	snapshot  func(ctx context.Context) []domain.PipelineEvent
	log       *slog.Logger
}

var _ ports.EventPublisher = (*Broker)(nil)

// NewBroker constructs a broker. snapshot (optional) supplies the events sent to a client on connect.
func NewBroker(snapshot func(ctx context.Context) []domain.PipelineEvent, log *slog.Logger) *Broker {
	if log == nil {
		log = slog.Default()
	}
	return &Broker{subs: map[chan domain.PipelineEvent]struct{}{}, heartbeat: sseHeartbeat, snapshot: snapshot, log: log}
}

// SetSnapshot installs the connect-time snapshot provider (breaks the broker<->pipeline
// construction cycle: the pipeline publishes to the broker, the broker reads pipeline stats).
func (b *Broker) SetSnapshot(fn func(ctx context.Context) []domain.PipelineEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snapshot = fn
}

// Publish implements ports.EventPublisher.
func (b *Broker) Publish(_ context.Context, ev domain.PipelineEvent) {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default: // subscriber is slow; drop
		}
	}
}

// Subscribers returns the number of connected clients.
func (b *Broker) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

func (b *Broker) subscribe() chan domain.PipelineEvent {
	ch := make(chan domain.PipelineEvent, sseBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *Broker) unsubscribe(ch chan domain.PipelineEvent) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

// ServeHTTP streams events as text/event-stream until the client disconnects.
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, b.log, fmt.Errorf("streaming unsupported"))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	ch := b.subscribe()
	defer b.unsubscribe(ch)

	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	b.mu.RLock()
	snap := b.snapshot
	b.mu.RUnlock()
	if snap != nil {
		for _, ev := range snap(r.Context()) {
			if err := b.write(w, ev); err != nil {
				return
			}
		}
	}
	fl.Flush()
	tick := time.NewTicker(b.heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			if err := b.write(w, ev); err != nil {
				return
			}
			fl.Flush()
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

func (b *Broker) write(w http.ResponseWriter, ev domain.PipelineEvent) error {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", b.seq.Add(1), ev.Type, data)
	return err
}
