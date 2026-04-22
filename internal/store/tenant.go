package store

import (
	"context"
	"errors"
)

// tenantKey is the context key type for the tenant ID. Unexported +
// typed so external code cannot collide with our value.
type tenantKey struct{}

// ErrMissingTenant is returned by TenantIDFromContext when the context
// carries no tenant (or an empty string). Consumers must surface this
// rather than silently writing rows with tenant_id="".
var ErrMissingTenant = errors.New("store: tenant id missing from context")

// WithTenantID returns a derived context tagged with the given tenant
// ID. Stores use this as the canonical routing key for every write.
// An empty id is allowed here (derivable via context.Value lookup) —
// TenantIDFromContext is what rejects empty values.
func WithTenantID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, tenantKey{}, id)
}

// TenantIDFromContext retrieves the tenant ID attached to ctx. Missing
// or empty value returns ErrMissingTenant. Go port of goclaw's
// TenantIDFromContext() god-node.
func TenantIDFromContext(ctx context.Context) (string, error) {
	v := ctx.Value(tenantKey{})
	id, ok := v.(string)
	if !ok || id == "" {
		return "", ErrMissingTenant
	}
	return id, nil
}

// MustTenantIDFromContext is the insert-side variant that panics when
// tenant is absent. Use only from code paths that are guaranteed by
// construction to run inside a tenant-scoped ctx (e.g. inside a
// request handler already guarded). Go port of goclaw's
// TenantIDForInsert() convention.
func MustTenantIDFromContext(ctx context.Context) string {
	id, err := TenantIDFromContext(ctx)
	if err != nil {
		panic(err)
	}
	return id
}
