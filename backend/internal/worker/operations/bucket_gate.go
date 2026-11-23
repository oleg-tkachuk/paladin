package operations

import (
	"context"
	"fmt"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
)

// This file holds how a batch executor asks the bucket gate (ADR-0015) when
// it runs. The submit RPC asked it already (batchh), but the batch runs
// later, on a worker, and the backend may have been disabled or set draining
// in between. So each executor asks again, through the same bucket resolver,
// before its first item.
//
// A refusal fails the operation with the gate's refusal, which the runner
// records with the refusal's reason as the operation's error code: the batch
// is the continuation of one request, and the answer is the one that request
// would have got had it come a moment later. It is not deferred: the runner
// has no way back to PENDING that would not claim the same operation again at
// once, and a batch left pending for as long as an operator keeps a backend
// disabled tells its client nothing.
//
// The gate is asked once, not per item. A backend disabled while a batch runs
// does not stop it; the items written before stay written, as the rows a
// single RPC wrote before the disable do.

// bucketResolver is the slice of the object repository the gate is asked
// through.
type bucketResolver interface {
	LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, access bucketgate.Access) (backendID, bucket string, err error)
}

// admitBatch asks the bucket gate whether collection's bucket takes access
// for op, before the batch's first item. A refusal is logged at Warn with the
// gate's reason and returned wrapped, so bucketgate.Refused still sees it; a
// lookup that failed is logged at Error and returned.
func admitBatch(ctx context.Context, buckets bucketResolver, log *zap.Logger, op operationh.Operation, collection string, access bucketgate.Access) error {
	_, _, err := buckets.LookupBucket(ctx, op.TenantID, collection, access)
	if err == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	fields := []zap.Field{
		zap.String("operation_id", op.OperationID.String()),
		zap.String("type", op.Type),
		zap.String("tenant_id", op.TenantID.String()),
		zap.String("collection", collection),
		zap.Stringer("access", access),
	}
	if bucketgate.Refused(err) {
		log.Warn("the bucket gate refuses the batch's bucket; the operation fails with its refusal",
			append(fields, zap.String("reason", err.Error()))...)
		return fmt.Errorf("collection %q: %w", collection, err)
	}
	log.Error("bucket lookup before the batch's first item failed", append(fields, zap.Error(err))...)
	return fmt.Errorf("lookup bucket: %w", err)
}
