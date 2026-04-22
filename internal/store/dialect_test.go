package store

import "testing"

func TestSQLiteDialect(t *testing.T) {
	d := NewSQLiteDialect()
	if d.Name() != "sqlite" {
		t.Errorf("Name: %q", d.Name())
	}
	for _, n := range []int{1, 2, 7, 100} {
		if got := d.Placeholder(n); got != "?" {
			t.Errorf("Placeholder(%d): %q, want ?", n, got)
		}
	}
	if d.SupportsReturning() {
		t.Error("SupportsReturning: want false for MVP")
	}
	if got := d.QuoteIdent("foo"); got != `"foo"` {
		t.Errorf("QuoteIdent(foo): %q", got)
	}
}
