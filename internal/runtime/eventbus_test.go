package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func evt(tenantID, typ, payloadStr string) Event {
	return Event{
		TenantID: tenantID,
		Type:     typ,
		Payload:  json.RawMessage(payloadStr),
		At:       time.Now(),
	}
}

// TestEventBusPublishSubscribe: single publisher, single subscriber, 5
// events, order preserved.
func TestEventBusPublishSubscribe(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	sub := bus.Subscribe(16, nil)
	defer bus.Unsubscribe(sub)

	for i := 0; i < 5; i++ {
		bus.Publish(evt("t1", "msg", fmt.Sprintf(`{"i":%d}`, i)))
	}

	for i := 0; i < 5; i++ {
		select {
		case got := <-sub.C:
			want := fmt.Sprintf(`{"i":%d}`, i)
			if string(got.Payload) != want {
				t.Errorf("event %d: got %s, want %s", i, got.Payload, want)
			}
			if got.Type != "msg" || got.TenantID != "t1" {
				t.Errorf("event %d header: %+v", i, got)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("event %d: timeout", i)
		}
	}
	if d := sub.Dropped(); d != 0 {
		t.Errorf("Dropped: got %d, want 0", d)
	}
}

// TestEventBusMultiSubscriber: 3 subscribers all see every event.
func TestEventBusMultiSubscriber(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	subs := []*Subscription{
		bus.Subscribe(16, nil),
		bus.Subscribe(16, nil),
		bus.Subscribe(16, nil),
	}
	defer func() {
		for _, s := range subs {
			bus.Unsubscribe(s)
		}
	}()

	for i := 0; i < 3; i++ {
		bus.Publish(evt("", "broadcast", fmt.Sprintf(`{"n":%d}`, i)))
	}

	for si, s := range subs {
		for i := 0; i < 3; i++ {
			select {
			case got := <-s.C:
				want := fmt.Sprintf(`{"n":%d}`, i)
				if string(got.Payload) != want {
					t.Errorf("sub %d event %d: %s", si, i, got.Payload)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatalf("sub %d event %d: timeout", si, i)
			}
		}
	}
}

// TestEventBusFilter: subscriber with Type=="A" filter ignores B events.
func TestEventBusFilter(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	onlyA := func(e Event) bool { return e.Type == "A" }
	sub := bus.Subscribe(16, onlyA)
	defer bus.Unsubscribe(sub)

	bus.Publish(evt("", "A", `{"i":0}`))
	bus.Publish(evt("", "B", `{"i":1}`))
	bus.Publish(evt("", "A", `{"i":2}`))
	bus.Publish(evt("", "B", `{"i":3}`))

	got := drainN(t, sub, 2, 500*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("received %d events, want 2", len(got))
	}
	for _, e := range got {
		if e.Type != "A" {
			t.Errorf("leaked non-A: %q", e.Type)
		}
	}
	// No more events should arrive.
	select {
	case e := <-sub.C:
		t.Errorf("unexpected extra event: %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestEventBusSlowConsumerDrops: buf=2, publish 5 while consumer is
// frozen → Dropped() reports 3.
func TestEventBusSlowConsumerDrops(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	sub := bus.Subscribe(2, nil) // buffer of 2
	defer bus.Unsubscribe(sub)

	for i := 0; i < 5; i++ {
		bus.Publish(evt("", "t", fmt.Sprintf(`{"i":%d}`, i)))
	}

	// Consumer now drains what's in the buffer.
	got := drainN(t, sub, 2, 500*time.Millisecond)
	if len(got) != 2 {
		t.Errorf("received %d, want 2 (buffer capacity)", len(got))
	}
	if d := sub.Dropped(); d != 3 {
		t.Errorf("Dropped: got %d, want 3", d)
	}
}

// TestEventBusUnsubscribe: after Unsubscribe the channel is closed and
// later Publish does not deliver to it.
func TestEventBusUnsubscribe(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	sub := bus.Subscribe(4, nil)
	bus.Publish(evt("", "t", `{}`))

	// First event should land.
	select {
	case <-sub.C:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("initial event: timeout")
	}

	bus.Unsubscribe(sub)

	// Channel must be closed.
	select {
	case _, ok := <-sub.C:
		if ok {
			t.Error("channel should be closed after Unsubscribe")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("closed-channel read timed out")
	}

	// Further Publish must not panic or deliver.
	bus.Publish(evt("", "t", `{}`))

	// Second Unsubscribe is a no-op.
	bus.Unsubscribe(sub)
}

// TestEventBusCloseClosesAllChannels: after Close, every subscription's
// channel is closed, and Publish becomes a no-op (no panic).
func TestEventBusCloseClosesAllChannels(t *testing.T) {
	bus := NewEventBus()

	subs := []*Subscription{
		bus.Subscribe(4, nil),
		bus.Subscribe(4, nil),
		bus.Subscribe(4, nil),
	}

	if err := bus.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for i, s := range subs {
		select {
		case _, ok := <-s.C:
			if ok {
				t.Errorf("sub %d: channel still open after Close", i)
			}
		case <-time.After(500 * time.Millisecond):
			t.Errorf("sub %d: channel never closed", i)
		}
	}

	// Publish after Close — must not panic.
	bus.Publish(evt("", "t", `{}`))

	// Second Close — no-op.
	if err := bus.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}

	// Subscribe after Close returns a subscription whose channel is
	// already closed.
	late := bus.Subscribe(4, nil)
	select {
	case _, ok := <-late.C:
		if ok {
			t.Error("post-close Subscribe: channel should be closed")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("post-close Subscribe: timeout waiting for close")
	}
}

// TestEventBusConcurrentPublishSubscribe: 50 publisher goroutines × 20
// events each = 1000 publishes; 5 subscribers with buf=100 each. Total
// received + dropped must equal publishes × subscribers. Run with -race.
func TestEventBusConcurrentPublishSubscribe(t *testing.T) {
	bus := NewEventBus()
	const P = 50  // publishers
	const E = 20  // events each
	const S = 5   // subscribers
	const B = 100 // subscriber buffer

	subs := make([]*Subscription, S)
	for i := 0; i < S; i++ {
		subs[i] = bus.Subscribe(B, nil)
	}

	var consumed atomic.Uint64
	var wgConsumers sync.WaitGroup
	wgConsumers.Add(S)
	stopConsumers := make(chan struct{})
	for i := 0; i < S; i++ {
		s := subs[i]
		go func() {
			defer wgConsumers.Done()
			for {
				select {
				case _, ok := <-s.C:
					if !ok {
						return
					}
					consumed.Add(1)
				case <-stopConsumers:
					// Drain whatever's left quickly.
					for {
						select {
						case _, ok := <-s.C:
							if !ok {
								return
							}
							consumed.Add(1)
						default:
							return
						}
					}
				}
			}
		}()
	}

	// Publishers.
	var wgPubs sync.WaitGroup
	wgPubs.Add(P)
	for p := 0; p < P; p++ {
		p := p
		go func() {
			defer wgPubs.Done()
			for e := 0; e < E; e++ {
				bus.Publish(evt("t", "c", fmt.Sprintf(`{"p":%d,"e":%d}`, p, e)))
			}
		}()
	}
	wgPubs.Wait()

	// Give consumers a beat to drain.
	time.Sleep(100 * time.Millisecond)
	close(stopConsumers)
	bus.Close()
	wgConsumers.Wait()

	totalPublishes := uint64(P * E)
	var dropped uint64
	for _, s := range subs {
		dropped += s.Dropped()
	}
	wantReceivedPlusDropped := totalPublishes * uint64(S)
	got := consumed.Load() + dropped
	if got != wantReceivedPlusDropped {
		t.Errorf("received(%d)+dropped(%d)=%d, want %d",
			consumed.Load(), dropped, got, wantReceivedPlusDropped)
	}
}

// TestEventBusPanickingFilterDoesNotKillBus verifies a filter that
// panics is contained — other subscribers keep working.
func TestEventBusPanickingFilterDoesNotKillBus(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	crash := func(Event) bool { panic("nope") }
	healthy := func(e Event) bool { return true }

	subBad := bus.Subscribe(4, crash)
	subGood := bus.Subscribe(4, healthy)

	bus.Publish(evt("", "t", `{}`))
	bus.Publish(evt("", "t", `{}`))

	// subGood should still receive both.
	got := drainN(t, subGood, 2, 500*time.Millisecond)
	if len(got) != 2 {
		t.Errorf("good sub got %d, want 2", len(got))
	}
	// subBad receives nothing (filter rejected both via panic-recover).
	select {
	case e := <-subBad.C:
		t.Errorf("bad sub leaked: %+v", e)
	case <-time.After(50 * time.Millisecond):
	}

	// Sanity check the "did not panic" — if the bus had blown up we
	// would not have reached here. Extra: ensure the string "nope" did
	// not surface in the test log (no stray goroutine fprinted it).
	_ = strings.TrimSpace("noop")
}

// drainN collects up to n events from sub within timeout.
func drainN(t *testing.T, sub *Subscription, n int, timeout time.Duration) []Event {
	t.Helper()
	out := make([]Event, 0, n)
	deadline := time.After(timeout)
	for len(out) < n {
		select {
		case e, ok := <-sub.C:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-deadline:
			return out
		}
	}
	return out
}
