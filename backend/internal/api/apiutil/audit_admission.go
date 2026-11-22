package apiutil

import (
	"context"
	"slices"
	"sync"
	"uuid"
)

// auditAdmissionKey holds the tenants a call's authorization path admitted
// it to act in, for the audit interceptor: the one place that says a row
// naming a tenant belongs in that tenant's trail
// (admindomain.TrailTenant). A mutable cell for the reason the resource slot
// is one (audit_stash.go): the interceptor installs it, the handler's
// authorization writes it, and the interceptor reads it after the handler
// returns.
type auditAdmissionKey struct{}

type auditAdmissionSlot struct {
	mu      sync.Mutex
	tenants []uuid.UUID
}

// WithAdmissionSlot attaches an empty admission slot to ctx. The audit
// interceptor calls it at the start of each request.
func WithAdmissionSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, auditAdmissionKey{}, &auditAdmissionSlot{})
}

// AdmitTenant records that the authorization path admitted this call to act
// in tenant: a policy decision that allowed it on a resource of that tenant
// (Decide), or a gate that let the caller cross into it. Call it only after
// the decision, with the tenant the decision was made on — never with a
// tenant read from the request before it. No-op for uuid.Nil or when ctx has
// no slot.
func AdmitTenant(ctx context.Context, tenant uuid.UUID) {
	if tenant == uuid.Nil() {
		return
	}
	slot, ok := ctx.Value(auditAdmissionKey{}).(*auditAdmissionSlot)
	if !ok || slot == nil {
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slices.Contains(slot.tenants, tenant) {
		slot.tenants = append(slot.tenants, tenant)
	}
}

// AdmittedTenants returns the tenants AdmitTenant recorded on ctx, nil for
// none.
func AdmittedTenants(ctx context.Context) []uuid.UUID {
	slot, ok := ctx.Value(auditAdmissionKey{}).(*auditAdmissionSlot)
	if !ok || slot == nil {
		return nil
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slices.Clone(slot.tenants)
}
