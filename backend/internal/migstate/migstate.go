// Package migstate names the states of a tenant's storage migration
// (ADR-0015), for the packages that must agree on them: the migration worker
// that drives them, and the object promote that waits on them. A leaf with no
// imports beyond the standard library, so the promote does not link the
// worker.
//
// tenant_storage_migrations.state is plain text with no CHECK, so this list
// is the only definition of the set.
package migstate

import (
	"hash/fnv"
	"uuid"
)

// Transaction-scoped advisory locks, in Postgres's two-key form: a space,
// naming what the lock serialises, and the tenant's key (TenantLockKey). The
// two-key form shares no keys with the one-key form other locks use
// (package objectpath).
const (
	// LockSpaceStart serialises a migration entering a fencing state from
	// a non-fencing one — a start, a retry, a stuck rollback — against the
	// tenant's promotes. A promote holds it shared before it reads the
	// migration's row, the transition exclusively: a promote that read no
	// fencing row has committed before the transition, and every later one
	// sees the row and holds it (FencingPromotes).
	LockSpaceStart int32 = 1501
)

// TenantLockKey is a tenant's key in every lock space. Two tenants may share
// one; they then wait on each other, which costs time and nothing else.
func TenantLockKey(tenantID uuid.UUID) int32 {
	h := fnv.New32a()
	_, _ = h.Write(tenantID[:])
	// A lock key: wrapping into the signed range is intended.
	return int32(h.Sum32()) // #nosec G115 -- a hash reinterpreted as a lock key; every bit pattern is a valid key
}

const (
	Provisioning = "provisioning"
	Copying      = "copying"
	Rebinding    = "rebinding"
	Verifying    = "verifying"
	Completed    = "completed"
	Cleaned      = "cleaned"
	Failed       = "failed"
	// RollingBack: the physical verify failed after the rebind; the worker
	// is putting the tenant back on its source bucket.
	RollingBack = "rolling_back"
)

// FencingPromotes are the states in which an object's promote holds its
// tenant's migration row (FOR SHARE) for the promote's transaction, so a
// step that takes the row FOR UPDATE waits for every promote in flight and
// holds back every later one:
//
//   - Provisioning: the copy's start (copy_started_at) is taken under the
//     row's lock, so every promote has committed before it or is stamped
//     after it.
//   - Copying and Rebinding: the rebind's check that every write since the
//     copy started reached the target runs under the row's lock, so no
//     promote commits unseen between the check and the rebind.
//   - Verifying and RollingBack: the rebind back to the source runs under
//     the row's lock, the same way.
//
// Completed and later move no binding, so no promote waits on them.
func FencingPromotes() []string {
	return []string{Provisioning, Copying, Rebinding, Verifying, RollingBack}
}

// ReboundToTarget are the states in which the rebind may have moved the
// tenant's private collections to the target while the source still holds
// what it held — Rebinding once the rebind has committed (rebound_at is set),
// the later ones always: a write a storage event reports in the source late,
// after the rebind, can still be moved into the target. Cleaned has emptied
// the source, and Failed is either before any rebind or after the rebind
// back.
func ReboundToTarget() []string {
	return []string{Rebinding, Verifying, RollingBack, Completed}
}
