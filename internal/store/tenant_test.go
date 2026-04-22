package store

import (
	"context"
	"errors"
	"testing"
)

func TestTenantIDFromContextMissing(t *testing.T) {
	_, err := TenantIDFromContext(context.Background())
	if !errors.Is(err, ErrMissingTenant) {
		t.Errorf("err: got %v, want ErrMissingTenant", err)
	}
}

func TestTenantIDFromContextEmpty(t *testing.T) {
	ctx := WithTenantID(context.Background(), "")
	_, err := TenantIDFromContext(ctx)
	if !errors.Is(err, ErrMissingTenant) {
		t.Errorf("empty id should be rejected, got %v", err)
	}
}

func TestWithTenantIDRoundtrip(t *testing.T) {
	ctx := WithTenantID(context.Background(), "acme")
	got, err := TenantIDFromContext(ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "acme" {
		t.Errorf("id: %q", got)
	}
}

func TestTenantIDOverride(t *testing.T) {
	// Outer ctx holds one tenant; inner WithTenantID shadows it.
	ctx := WithTenantID(context.Background(), "outer")
	ctx = WithTenantID(ctx, "inner")
	got, err := TenantIDFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "inner" {
		t.Errorf("id: %q, want inner", got)
	}
}

func TestMustTenantIDFromContextPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}
	}()
	_ = MustTenantIDFromContext(context.Background())
}

func TestMustTenantIDFromContextReturns(t *testing.T) {
	ctx := WithTenantID(context.Background(), "ok")
	if got := MustTenantIDFromContext(ctx); got != "ok" {
		t.Errorf("id: %q", got)
	}
}
