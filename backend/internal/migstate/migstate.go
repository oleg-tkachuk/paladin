// Package migstate names the states of a tenant's storage migration
// (ADR-0015), for the packages that must agree on them: the migration worker
// that drives them, and the object promote that waits on them. A leaf with no
// imports, so the promote does not link the worker.
//
// tenant_storage_migrations.state is plain text with no CHECK, so this list
// is the only definition of the set.
package migstate

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
