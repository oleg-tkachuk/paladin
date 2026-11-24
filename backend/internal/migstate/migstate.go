// Package migstate names the states of a tenant's storage migration
// (ADR-0015, ADR-0051), for the packages that must agree on them: the
// migration worker that drives them, the repository that writes them and the
// purge that owes their buckets. A leaf with no imports, so none of them
// links another.
//
// tenant_storage_migrations.state is plain text with no CHECK, so this list
// is the only definition of the set.
package migstate

const (
	// Provisioning: the target bucket is being created; nothing else changed.
	Provisioning = "provisioning"
	// Moving: new uploads go to the target, and each pass moves the objects
	// placed in the source whose upload anchor has settled.
	Moving = "moving"
	// Completed: nothing is left in the source and no URL can write it; its
	// copies are kept until cleanup_after.
	Completed = "completed"
	// Cleaned: the source copies are deleted. Terminal.
	Cleaned = "cleaned"
	// Failed: provisioning failed, or the rollback finished. Retryable. After
	// a rollback each pass still moves back what a request that read the
	// target binding before the switch back left in the target, until the
	// migration settles; a provisioning failure is settled as it fails.
	Failed = "failed"
	// RollingBack: new uploads go to the source again, and each pass moves
	// the objects placed in the target back.
	RollingBack = "rolling_back"
)

// MaxFailedPasses is how many passes in a row may end with a move that
// failed its verification before the migration rolls back: a copy that keeps
// coming out wrong is the target's, or the store's, and waiting will not
// mend it.
const MaxFailedPasses = 5

// Owing are the states in which an object of the tenant may have a copy in
// either of the migration's buckets, so a purge owes both: every state but
// Cleaned, which has deleted the source copies. Failed included: a rollback
// leaves its moves' copies in the target.
func Owing() []string {
	return []string{Provisioning, Moving, Completed, RollingBack, Failed}
}
