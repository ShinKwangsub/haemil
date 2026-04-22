package runtime

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// Event is the unit an EventBus carries. All fields are plain values —
// no back-references to publishers, no hidden ownership. Copy-by-value
// is fine.
type Event struct {
	// TenantID is the publisher's tenant (C10 semantics) or "" for
	// system-level events. Routing to a specific target tenant is a
	// subscriber concern (apply a Filter), not an Event field.
	TenantID string
	// Type is a free-form string picked by the publisher, e.g.
	// "turn.completed". Subscribers match on it via Filter.
	Type string
	// Payload is opaque bytes — usually JSON — whose shape is a
	// per-Type contract between publisher and consumers.
	Payload json.RawMessage
	// At is when Publish was called.
	At time.Time
}

// Filter decides whether a subscriber cares about an event. A nil
// Filter matches every event. A Filter that panics is recovered from
// and treated as "no match"; other subscribers are unaffected.
type Filter func(Event) bool

// Subscription is the subscriber-facing handle. Read from C. The
// channel is closed when Unsubscribe runs or the EventBus is Close()d.
// Dropped() reports how many events were dropped because C was full
// at delivery time (per-subscriber slow-consumer counter).
type Subscription struct {
	// C is the read-only view of the internal buffered channel.
	C <-chan Event

	// internal
	ch      chan Event
	filter  Filter
	dropped atomic.Uint64

	// unsub bookkeeping (bus pointer + index management lives on bus)
	bus *EventBus
}

// Dropped returns the number of events that were silently dropped for
// this subscription because its buffer was full when Publish tried to
// deliver. Observational counter — not reset by read.
func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }

// EventBus is an in-memory fire-and-forget publish/subscribe hub. See
// package-level Phase 4 C12 design notes:
//
//   - Publish never blocks. If a subscriber's buffer is full, the event
//     is dropped for that subscriber and its Dropped() counter
//     increments. Other subscribers are unaffected.
//   - Per-subscriber FIFO: a given subscriber receives events in the
//     same order they were Published.
//   - Close closes every subscription's channel and makes future
//     Publish calls no-ops (not panics). Safe to call multiple times.
//   - Filter runs on the subscriber side — the bus fans every event out
//     to every live subscription, and each subscription's Filter
//     decides whether to accept. This keeps the bus trivial.
//
// EventBus is safe for concurrent use. It holds no goroutines of its
// own; delivery happens synchronously on the Publish caller's stack.
type EventBus struct {
	mu     sync.RWMutex
	subs   []*Subscription
	closed atomic.Bool
}

// DefaultSubscriberBuffer is used when Subscribe is called with buf<=0.
// Tuned small on purpose — callers who expect high-frequency topics
// should size their buffer to match their consumer loop's latency
// budget.
const DefaultSubscriberBuffer = 16

// NewEventBus returns an empty, ready-to-use EventBus.
func NewEventBus() *EventBus {
	return &EventBus{}
}

// Subscribe registers a new subscription and returns its handle. buf is
// the per-subscriber channel capacity; 0 (or negative) becomes
// DefaultSubscriberBuffer. filter may be nil (match all).
//
// The returned Subscription is live immediately. Subscribing after
// Close returns a Subscription whose channel is already closed — the
// caller observes this by a nil read or closed-channel read.
func (b *EventBus) Subscribe(buf int, filter Filter) *Subscription {
	if buf <= 0 {
		buf = DefaultSubscriberBuffer
	}
	ch := make(chan Event, buf)
	sub := &Subscription{
		C:      ch,
		ch:     ch,
		filter: filter,
		bus:    b,
	}
	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		close(ch)
		return sub
	}
	b.subs = append(b.subs, sub)
	b.mu.Unlock()
	return sub
}

// Unsubscribe removes a subscription and closes its channel. Idempotent
// — calling twice is a no-op, and a nil or foreign-bus subscription is
// silently ignored.
func (b *EventBus) Unsubscribe(s *Subscription) {
	if s == nil || s.bus != b {
		return
	}
	b.mu.Lock()
	for i, existing := range b.subs {
		if existing == s {
			// Swap-delete; order doesn't matter for fan-out.
			b.subs[i] = b.subs[len(b.subs)-1]
			b.subs = b.subs[:len(b.subs)-1]
			b.mu.Unlock()
			close(s.ch)
			return
		}
	}
	b.mu.Unlock()
}

// Publish delivers e to every live subscription whose Filter accepts it.
// Never blocks (slow subscribers get their event dropped). No-op after
// Close. Safe to call concurrently.
func (b *EventBus) Publish(e Event) {
	if b.closed.Load() {
		return
	}
	b.mu.RLock()
	// Snapshot — Publish must not hold the lock across subscriber
	// channel sends (would deadlock with Unsubscribe + block
	// progress). Holding RLock for the copy is cheap.
	subs := make([]*Subscription, len(b.subs))
	copy(subs, b.subs)
	b.mu.RUnlock()

	for _, s := range subs {
		if !matches(s.filter, e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			// Slow consumer. Record and move on — never block the
			// publisher. Other subscribers stay on time.
			s.dropped.Add(1)
		}
	}
}

// matches runs filter safely. A panicking filter is treated as
// "no match" for this event — the bus keeps running.
func matches(f Filter, e Event) (accept bool) {
	if f == nil {
		return true
	}
	defer func() {
		if r := recover(); r != nil {
			accept = false
		}
	}()
	return f(e)
}

// Close closes every live subscription's channel and prevents future
// Publish calls from delivering. Safe to call multiple times — second
// and later calls are no-ops.
func (b *EventBus) Close() error {
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		close(s.ch)
	}
	b.subs = nil
	return nil
}
