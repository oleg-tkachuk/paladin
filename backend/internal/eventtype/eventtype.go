// Package eventtype names the CloudEvents types that more than one component
// must spell the same: the producer that emits one and the code that treats
// it apart. A leaf with no imports, so either side can take it.
package eventtype

// TenantCreated is emitted on the transaction that creates a tenant.
const TenantCreated = "paladin.tenant.created"

// TenantUpdated is emitted on the transaction that changes a tenant.
const TenantUpdated = "paladin.tenant.updated"

// TenantRestored is emitted on the transaction that takes a tenant out of the
// trash.
const TenantRestored = "paladin.tenant.restored"

// TenantTrashed is emitted when a tenant moves to the trash. The outbox
// delivers it although the tenant is frozen: it is the event that says so.
const TenantTrashed = "paladin.tenant.trashed"

// CapabilityCharged is emitted on a charge's transaction for every charge a
// capability takes, when charge events are on.
const CapabilityCharged = "paladin.capability.charged"

// CapabilityRefunded is emitted on a refund's transaction for every refund
// that returns spend, naming the charge it returns from, so a subscriber
// summing CapabilityCharged events can take it back out.
const CapabilityRefunded = "paladin.capability.refunded"
