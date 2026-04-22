package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEventLogAppendMissingTenant(t *testing.T) {
	s := openMemory(t)
	log := NewEventLog(s)

	_, err := log.Append(context.Background(), "t", []byte(`{}`))
	if !errors.Is(err, ErrMissingTenant) {
		t.Errorf("err: %v, want ErrMissingTenant", err)
	}
}

func TestEventLogAppendBasic(t *testing.T) {
	s := openMemory(t)
	log := NewEventLog(s)

	ctx := WithTenantID(context.Background(), "acme")
	ev, err := log.Append(ctx, "turn.completed", []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if ev.TenantID != "acme" {
		t.Errorf("TenantID: %q", ev.TenantID)
	}
	if ev.Type != "turn.completed" {
		t.Errorf("Type: %q", ev.Type)
	}
	if string(ev.Payload) != `{"x":1}` {
		t.Errorf("Payload: %s", ev.Payload)
	}
	if ev.ID == "" {
		t.Error("ID empty")
	}
	if len(ev.ID) != 26 {
		t.Errorf("ID length: %d, want 26 (base32(16 bytes))", len(ev.ID))
	}

	// Readable via Since.
	rows, err := log.Since(ctx, time.Time{}, 10)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: %d, want 1", len(rows))
	}
	if rows[0].ID != ev.ID {
		t.Errorf("ID mismatch")
	}
}

// TestEventLogTenantIsolation is C11's acceptance test. Two tenants
// write to the same DB; each sees only its own rows.
func TestEventLogTenantIsolation(t *testing.T) {
	s := openMemory(t)
	log := NewEventLog(s)

	ctxA := WithTenantID(context.Background(), "tenant-A")
	ctxB := WithTenantID(context.Background(), "tenant-B")

	for i := 0; i < 10; i++ {
		if _, err := log.Append(ctxA, "a.evt", []byte(fmt.Sprintf(`{"i":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		if _, err := log.Append(ctxB, "b.evt", []byte(fmt.Sprintf(`{"i":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}

	rowsA, err := log.Since(ctxA, time.Time{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	rowsB, err := log.Since(ctxB, time.Time{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsA) != 10 {
		t.Errorf("tenant A rows: %d, want 10", len(rowsA))
	}
	if len(rowsB) != 5 {
		t.Errorf("tenant B rows: %d, want 5", len(rowsB))
	}
	for _, r := range rowsA {
		if r.TenantID != "tenant-A" {
			t.Errorf("A leaked: %+v", r)
		}
		if r.Type != "a.evt" {
			t.Errorf("A type leaked: %q", r.Type)
		}
	}
	for _, r := range rowsB {
		if r.TenantID != "tenant-B" {
			t.Errorf("B leaked: %+v", r)
		}
	}

	// CountAll ignores tenant — 15 total.
	total, err := log.CountAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if total != 15 {
		t.Errorf("CountAll: %d, want 15", total)
	}
}

func TestEventLogSinceTimeFilter(t *testing.T) {
	s := openMemory(t)
	log := NewEventLog(s)
	ctx := WithTenantID(context.Background(), "t")

	// Three rows, with a small sleep between so timestamps differ.
	ev1, _ := log.Append(ctx, "e", []byte(`1`))
	time.Sleep(10 * time.Millisecond)
	ev2, _ := log.Append(ctx, "e", []byte(`2`))
	time.Sleep(10 * time.Millisecond)
	_, _ = log.Append(ctx, "e", []byte(`3`))

	// Since(ev1.CreatedAt) → 3 rows (including ev1)
	all, err := log.Since(ctx, ev1.CreatedAt, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("Since from ev1: %d, want 3", len(all))
	}

	// Since(ev2.CreatedAt) → 2 rows (ev2 + ev3)
	after1, err := log.Since(ctx, ev2.CreatedAt, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(after1) != 2 {
		t.Errorf("Since from ev2: %d, want 2", len(after1))
	}

	// Since(now) → 0 rows
	future, err := log.Since(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(future) != 0 {
		t.Errorf("Since future: %d, want 0", len(future))
	}
}

func TestEventLogSinceLimit(t *testing.T) {
	s := openMemory(t)
	log := NewEventLog(s)
	ctx := WithTenantID(context.Background(), "t")

	for i := 0; i < 20; i++ {
		if _, err := log.Append(ctx, "e", []byte(fmt.Sprintf(`%d`, i))); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := log.Since(ctx, time.Time{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Errorf("limit=5: got %d", len(rows))
	}
}

// TestEventLogConcurrentAppendRace: 10 goroutines × 10 appends each =
// 100 total. Use file-backed DB so the busy_timeout path is exercised.
// With -race we catch any shared-state mistake.
func TestEventLogConcurrentAppendRace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	s, err := Open(context.Background(), "sqlite://"+path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	log := NewEventLog(s)

	ctx := WithTenantID(context.Background(), "racer")
	const G, N = 10, 10
	var wg sync.WaitGroup
	wg.Add(G)
	errs := make(chan error, G*N)
	for g := 0; g < G; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < N; i++ {
				if _, err := log.Append(ctx, "race", []byte(fmt.Sprintf(`{"g":%d,"i":%d}`, g, i))); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("append error: %v", e)
	}

	total, err := log.CountAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if total != G*N {
		t.Errorf("total: %d, want %d", total, G*N)
	}
}

// TestEventLogIDsUnique: 1000 IDs must not collide even under a tight
// loop where ms-prefix is identical.
func TestEventLogIDsUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id, err := newEventID()
		if err != nil {
			t.Fatal(err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID at i=%d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}
