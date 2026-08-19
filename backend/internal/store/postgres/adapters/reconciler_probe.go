package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin-private/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres/sqlc"
)

// HeadProber is the storage-side seam: HEAD an S3 object given the routing
// triplet. Satisfied by *s3adapter.Client (which already has Head with the
// same shape) and by the ObjectRouter that fronts it.
//
// The seam is about the CALL, not the package: it keeps a unit test from
// having to stand up an S3 client. The package does import s3adapter — for
// ErrObjectNotFound, and only for that. That is the deliberate half of the
// trade: one sentinel comparison here beats re-deriving "is this a 404?"
// from AWS error types in the persistence layer, which is what the stub this
// replaced was written to avoid.
type HeadProber interface {
	Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, objectKey, key string) (etag string, sizeBytes int64, checksum, sequencer string, err error)
}

// ReconcilerProbe satisfies worker.StorageProbe. It looks up the object's
// (tenant, bucket, key) routing in one query, then calls the storage HEAD.
// Missing rows → found=false, no error.
type ReconcilerProbe struct {
	q    *sqlc.Queries
	head HeadProber
}

func NewReconcilerProbe(q *sqlc.Queries, head HeadProber) *ReconcilerProbe {
	return &ReconcilerProbe{q: q, head: head}
}

// HeadByObjectID resolves routing + HEADs the bytes.
func (r *ReconcilerProbe) HeadByObjectID(ctx context.Context, objectID uuid.UUID) (etag string, sizeBytes int64, checksum, sequencer string, found bool, err error) {
	row, err := r.q.LookupObjectByID(ctx, pgUUID(objectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, "", "", false, nil
		}
		return "", 0, "", "", false, fmt.Errorf("lookup object: %w", err)
	}
	tenantID := uuidFrom(row.TenantID)
	// LookupObjectByID already materializes the backend binding, so the HEAD
	// is routed to the object's own backend (not the default).
	etag, sizeBytes, checksum, sequencer, err = r.head.Head(ctx, row.BackendID, row.BucketName, tenantID, row.ObjectKey, row.Key)
	if err != nil {
		// HEAD failure is "not found" if the storage adapter signals 404 via
		// the standard not-found error wrapping; treat as not-found here.
		// Other errors propagate so the worker can log them.
		if isNotFoundErr(err) {
			return "", 0, "", "", false, nil
		}
		return "", 0, "", "", false, err
	}
	return etag, sizeBytes, checksum, sequencer, true, nil
}

// isNotFoundErr reports whether a HEAD failure means the object genuinely is
// not on the backend, as opposed to the backend being unreachable, slow, or
// refusing us.
//
// The distinction is terminal: a true here sends ReconcilerV2 down the
// MarkFailed branch, and FAILED objects stop being served and become
// eligible for reclamation. So the classification deliberately lives in
// s3adapter, next to the SDK error shapes it has to reason about, and this
// stays a single errors.Is — no AWS types in the persistence layer, and no
// second place where "is this a 404?" can be got subtly wrong.
//
// This used to be a `return false` stub, which made MarkFailed unreachable:
// every object whose bytes were genuinely absent logged a HEAD warning and
// was retried on the next tick, forever.
func isNotFoundErr(err error) bool {
	return errors.Is(err, s3adapter.ErrObjectNotFound)
}
