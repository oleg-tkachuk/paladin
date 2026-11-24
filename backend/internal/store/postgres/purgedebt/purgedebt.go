// Package purgedebt records what a purge owes a storage migration in flight.
//
// A purge deletes an object's row and records its bytes as a debt in
// pending_purges, for the bucket the object is placed in. While the tenant
// migrates (ADR-0051) the bytes may also be in the migration's other bucket:
// a move leaves the source's copy until cleanup, and a rollback's move the
// target's. Neither the cleanup nor a later attempt will find them again —
// both walk object rows, and the row is gone — so the purge itself records
// them, on the transaction that deletes the row: a debt per bucket, which the
// purge drainer deletes and retries like any other. Recorded with the
// delete, they survive a crash at any step after it, and a delete of bytes
// already gone succeeds.
//
// Both purge paths call it: the lifecycle hard delete and a permanent
// delete. It runs after the row's DELETE on the same transaction: a move
// holds the row while it copies (FOR KEY SHARE), so a purge either waits for
// the move and then owes the copy it made, or ran first and leaves nothing to
// move.
package purgedebt

import (
	"context"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/migstate"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Purge is one object's purge: whose, and where its row said its bytes were.
type Purge struct {
	TenantID uuid.UUID
	ObjectID uuid.UUID
	// BackendID and Bucket are the bucket the object was placed in, which
	// the purge's own debt names.
	BackendID  string
	Bucket     string
	Collection string
	Key        string
}

// RecordMigrationCopies records p's debts for the copies a storage
// migration in flight may have left in its other bucket. It returns how many
// debts it recorded: none when the tenant is not migrating.
func RecordMigrationCopies(ctx context.Context, q *sqlc.Queries, p Purge) (int64, error) {
	n, err := q.InsertMigrationPurgeDebts(ctx, pgtype.UUID{Bytes: p.ObjectID, Valid: true},
		p.Collection, p.Key, p.BackendID, p.Bucket, pgtype.UUID{Bytes: p.TenantID, Valid: true}, migstate.Owing())
	if err != nil {
		return 0, fmt.Errorf("record the migration's copies as purge debt: %w", err)
	}
	return n, nil
}
