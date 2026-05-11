package apiutil

import (
	"context"
	"sync"
)

// auditResourceKey is the ctx-key handlers use to stash a canonical
// resource name for the audit interceptor. Defined here (rather than
// in `middleware/audit.go`) so domain handlers can call StashResource
// without importing the middleware package — that would invert the
// dependency direction (middleware sits above handlers).
//
// The value behind the key is a *mutable cell* (auditResourceSlot)
// rather than a plain string. The middleware attaches an empty slot
// before invoking the handler; the handler writes into the slot via
// StashResource; the middleware reads from the same slot after the
// handler returns. context.WithValue derives a new ctx and doesn't
// propagate back up — the mutable-cell pattern is what makes
// after-call middleware see the handler's write.
type auditResourceKey struct{}

type auditResourceSlot struct {
	mu   sync.Mutex
	name string
}

// WithResourceSlot attaches an empty resource-name slot to ctx. The
// audit middleware calls this at the start of each request; handlers
// then write into the slot via StashResource and the middleware
// reads via ResourceFromContext after the handler returns.
func WithResourceSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, auditResourceKey{}, &auditResourceSlot{})
}

// StashResource records a handler-computed canonical resource name
// (storageBackends/{b}/buckets/{bk}/tenants/{tid}/objectKeys/{ok}).
// No-op when the ctx has no slot attached (e.g. test paths that
// bypass the audit middleware) or when `canonical` is empty.
func StashResource(ctx context.Context, canonical string) {
	if canonical == "" {
		return
	}
	if slot, ok := ctx.Value(auditResourceKey{}).(*auditResourceSlot); ok && slot != nil {
		slot.mu.Lock()
		slot.name = canonical
		slot.mu.Unlock()
	}
}

// ResourceFromContext returns the stashed canonical resource name, or
// "" when the handler didn't stash one.
func ResourceFromContext(ctx context.Context) string {
	slot, ok := ctx.Value(auditResourceKey{}).(*auditResourceSlot)
	if !ok || slot == nil {
		return ""
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.name
}
