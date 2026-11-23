// Package purgedebt records what a purge owes a storage migration in flight.
//
// A purge deletes an object's row and records its bytes as a debt in
// pending_purges, for the bucket the collection is bound to. While the
// tenant migrates (ADR-0015) the bytes may also be in the migration's other
// bucket: the copy put them in the target, the rebind left them in the source
// until cleanup. Neither the copy nor the cleanup will find them again — both
// walk object rows, and the row is gone — so the purge itself records them,
// on the transaction that deletes the row: a debt per bucket, which the purge
// drainer deletes and retries like any other. Recorded with the delete, they
// survive a crash at any step after it, and a delete of bytes already gone
// succeeds.
//
// The purge also takes a not-yet-copied object off the copy's count, so a
// copy that ends short of it by the purged objects is not taken for one that
// missed some.
//
// Both purge paths call it: the lifecycle hard delete and a permanent
// delete. It runs after the row's DELETE on the same transaction: the copy
// holds the row while it copies (FOR KEY SHARE) and then updates the
// migration, so deleting the row first and updating the migration second
// takes the two locks in the copy's order.
package purgedebt

import (
	"context"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Purge is one object's purge: whose, where its row said its bytes were, and
// the state the row was in.
type Purge struct {
	TenantID uuid.UUID
	ObjectID uuid.UUID
	// BackendID and Bucket are the collection's binding, which the purge's
	// own debt names.
	BackendID  string
	Bucket     string
	Collection string
	Key        string
	// State is the purged row's state; only a live or trashed object is in
	// the copy's count.
	State string
}

// RecordMigrationCopies records p's debts for the copies a storage
// migration in flight made, and takes p off the copy's count when the copy
// had not reached it. It returns how many debts it recorded: none when the
// tenant is not migrating.
func RecordMigrationCopies(ctx context.Context, q *sqlc.Queries, p Purge) (int64, error) {
	tenant := pgtype.UUID{Bytes: p.TenantID, Valid: true}
	n, err := q.InsertMigrationPurgeDebts(ctx, pgtype.UUID{Bytes: p.ObjectID, Valid: true},
		p.Collection, p.Key, p.BackendID, p.Bucket, tenant)
	if err != nil {
		return 0, fmt.Errorf("record the migration's copies as purge debt: %w", err)
	}
	if err := q.ForgetPurgedFromStorageMigrationCount(ctx, p.State, tenant, p.Collection, p.Key); err != nil {
		return 0, fmt.Errorf("take the purged object off the migration's count: %w", err)
	}
	return n, nil
}
