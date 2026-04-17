package server

import (
	"sync"
)

// Event is the envelope broadcast through the Hub to every connected
// WebSocket client. Kind is a stable string ("scan:progress",
// "library:updated", "thumbnail:ready") that the frontend switches on;
// Payload is any JSON-encodable value whose shape is owned by the
// producer.
type Event struct {
	Kind    string `json:"kind"`
	Payload any    `json:"payload"`
}

// subscriberBufferSize caps the per-client backlog. A slow consumer
// blocks only itself: once its buffer fills, Broadcast drops the event
// for that subscriber rather than stalling the whole fan-out.
const subscriberBufferSize = 64

// Hub fans Events out to every currently-subscribed client. Producers
// (scanner, pre-fetcher, persistence) hold a *Hub and call Broadcast;
// the WebSocket handler is the sole consumer that calls Subscribe.
//
// The design trades reliability for isolation: dropping an event for a
// slow subscriber is preferred over blocking the producer. Events are
// progress/state hints that the frontend can recover from; nothing
// here is authoritative state.
type Hub struct {
	mu   sync.RWMutex
	subs map[*subscriber]struct{}
}

type subscriber struct {
	ch chan Event
}

// NewHub returns an empty Hub ready for concurrent use.
func NewHub() *Hub {
	return &Hub{subs: make(map[*subscriber]struct{})}
}

// Subscribe registers a new consumer and returns its event channel
// plus an unsubscribe function. The caller must invoke unsubscribe
// exactly once (typically with defer) so the Hub can release its slot
// and close the channel.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	s := &subscriber{ch: make(chan Event, subscriberBufferSize)}

	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		if _, ok := h.subs[s]; ok {
			delete(h.subs, s)
			close(s.ch)
		}
		h.mu.Unlock()
	}
	return s.ch, unsubscribe
}

// Publish sends the given event to every current subscriber. Slow
// subscribers whose buffer is full drop this event; the Hub never
// blocks the producer. Safe to call from any goroutine.
func (h *Hub) Publish(kind string, payload any) {
	h.Broadcast(Event{Kind: kind, Payload: payload})
}

// Broadcast is Publish without the ergonomic wrapper — useful when the
// caller already has an Event value on hand.
func (h *Hub) Broadcast(e Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs {
		select {
		case s.ch <- e:
		default:
			// Drop for this subscriber; it's fallen behind.
		}
	}
}

// SubscriberCount reports how many clients are currently connected.
// Handy for tests and /healthz-style introspection.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}
