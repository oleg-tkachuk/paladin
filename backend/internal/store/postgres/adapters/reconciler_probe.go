package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// HeadProber is the storage-side seam: HEAD an S3 object given the routing
// triplet. Satisfied by *s3adapter.Client (which already has Head with the
// same shape). Defined here so the adapter package owns the worker-side
// translation without forcing a dependency on s3adapter.
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

// isNotFoundErr is a tiny heuristic: storage adapters wrap S3 NotFound /
// NoSuchKey distinctly per backend. We only need to know *whether* the
// reconciler should mark the object FAILED — and that's the case for any
// error that isn't transient. Conservative: treat all errors as transient
// (return them) except when we can clearly attribute to "missing".
func isNotFoundErr(err error) bool {
	// Avoid pulling AWS SDK error types here. The s3adapter package is
	// expected to wrap NotFound/NoSuchKey using a sentinel; until that
	// is added, we degrade open and let the reconciler log+skip.
	return false
}
