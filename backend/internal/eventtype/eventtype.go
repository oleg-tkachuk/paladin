// Package eventtype names the CloudEvents types that more than one component
// must spell the same: the producer that emits one and the code that treats
// it apart. A leaf with no imports, so either side can take it.
package eventtype

// TenantTrashed is emitted when a tenant moves to the trash. The outbox
// delivers it although the tenant is frozen: it is the event that says so.
const TenantTrashed = "paladin.tenant.trashed"
