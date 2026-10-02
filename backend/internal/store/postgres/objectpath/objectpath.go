// Package objectpath keeps a byte purge off a storage key that a new object
// has taken.
//
// An object's bytes live at a key derived from tenant, collection name and
// path — not from the object's id. A permanent delete removes the row first
// and owes the bytes to pending_purges; once the row is gone the path is free,
// and a new object uploaded there writes the same key. Deleting "the old
// bytes" after that deletes the new object's.
//
// Two rules close it. Every object row is inserted under Lock, and every
// purge decides under Check, on the transaction that then issues the storage
// delete. A row is always inserted before its bytes are written, so a purge
// that finds no row while holding the lock deletes bytes nobody owns, and an
// upload that starts after it writes after the delete.
package objectpath

import (
	"context"
	"errors"
	"hash/fnv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// keySeparator joins the key's parts. Postgres text cannot hold NUL, so no
// part contains it and two different paths never join to the same string.
const keySeparator = "\x00"

// lockKey is the advisory-lock key for one storage path. The collection is
// named, not identified: a collection deleted and created again has a new id
// but the same storage prefix.
func lockKey(tenantID uuid.UUID, collection, path string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(tenantID.String() + keySeparator + collection + keySeparator + path))
	// A lock key: wrapping into the signed range is intended.
	return int64(h.Sum64())
}

// Lock waits for the path's lock on q's transaction. Object inserts take it,
// so none lands while a purge of the same path is deciding.
func Lock(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, collection, path string) error {
	return q.LockObjectPath(ctx, lockKey(tenantID, collection, path))
}

// Verdict is what a purge may do with the bytes at a path.
type Verdict int

const (
	// Delete: no row owns the path; the bytes are the purge's to delete.
	Delete Verdict = iota
	// Superseded: an object whose bytes were written owns the path. Its write
	// replaced the bytes the purge was for, so the debt is paid without a
	// delete — which would remove that object's bytes.
	Superseded
	// Defer: an upload may be writing the path, or another transaction holds
	// its lock. Leave the debt for a later attempt.
	Defer
)

// Check decides a purge of the bytes at a path. It must run on the
// transaction that then issues the storage delete: the lock it takes is held
// until that transaction ends. It never waits, so a purge cannot stall behind
// an upload or deadlock with another purge.
func Check(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, collection, path string) (Verdict, error) {
	locked, err := q.TryLockObjectPath(ctx, lockKey(tenantID, collection, path))
	if err != nil {
		return Defer, err
	}
	if !locked {
		return Defer, nil
	}
	state, err := q.ObjectStateAtPath(ctx, pgtype.UUID{Bytes: tenantID, Valid: true}, collection, path)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delete, nil
	}
	if err != nil {
		return Defer, err
	}
	switch state {
	case sqlc.ObjectStateAVAILABLE, sqlc.ObjectStateDELETED:
		// Promoted only once its bytes were confirmed, so they were written.
		return Superseded, nil
	default:
		return Defer, nil
	}
}
