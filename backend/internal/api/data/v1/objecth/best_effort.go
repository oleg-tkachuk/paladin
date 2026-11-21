package objecth

import (
	"context"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// msgBestEffortFailed is the line a failed best-effort read leaves: the RPC
// goes on without what it read, and nothing else would say why.
const msgBestEffortFailed = "best-effort read failed; going on without it"

// fieldRead names which best-effort read failed.
const fieldRead = "read"

// The best-effort reads, as their warning names them.
const (
	// readScopeBucket is the collection's (backend, bucket), resolved only to
	// scope an authorization. Without it a bucket:- or collection:-scoped
	// token is denied and an unscoped caller is unaffected.
	readScopeBucket = "scope bucket"
	// readCanonicalPrefix is the binding an event's resource name is built
	// from. Without it the event carries the tenant-first name.
	readCanonicalPrefix = "canonical prefix"
)

// warnBestEffort records that read failed under op, for tenantID's
// collection, on l — or on the context's logger when the handler has none.
func warnBestEffort(ctx context.Context, l *zap.Logger, op, read string, tenantID uuid.UUID, collection string, err error) {
	if l == nil {
		l = logger.FromContext(ctx)
	}
	l.Warn(msgBestEffortFailed,
		zap.String("op", op), zap.String(fieldRead, read),
		zap.String("tenant_id", tenantID.String()), zap.String(fieldCollection, collection),
		zap.Error(err))
}

// scopeBucket resolves collection's (backend, bucket) for a READ's
// authorization scope, best-effort: a failure is warned about and answers
// empty, which leaves a scoped principal fail-closed and an unscoped one
// unaffected. A write never comes here: writeBinding resolves it through the
// write gate and fails closed.
func scopeBucket(ctx context.Context, l *zap.Logger, repo Repository, op string, tenantID uuid.UUID, collection string) (backendID, bucket string) {
	backendID, bucket, err := repo.LookupBucket(ctx, tenantID, collection, false)
	if err != nil {
		warnBestEffort(ctx, l, op, readScopeBucket, tenantID, collection, err)
		return "", ""
	}
	return backendID, bucket
}
