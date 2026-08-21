package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// ObjectLockRepo persists object_locks rows (ADR-0013).
//
// The retention rules are not implemented here — they live in the WHERE
// clause of SetObjectRetention. That is deliberate: a read-then-write in Go
// would let two concurrent callers each observe a long COMPLIANCE window and
// each conclude their shorter write is a valid extension. Expressed as a
// conditional upsert, the second one simply matches no row, and this file's
// only job is to translate that into ErrRetentionWeakened.
type ObjectLockRepo struct {
	q *sqlc.Queries
}

func NewObjectLockRepo(q *sqlc.Queries) *ObjectLockRepo {
	return &ObjectLockRepo{q: q}
}

var _ object.LockRepository = (*ObjectLockRepo)(nil)

func (r *ObjectLockRepo) SetRetention(ctx context.Context, args object.SetRetentionArgs) (object.ObjectLock, error) {
	row, err := r.q.SetObjectRetention(ctx,
		pgUUID(args.TenantID),
		pgUUID(args.VersionID),
		sqlc.ObjectLockMode(args.Mode),
		pgtype.Timestamptz{Time: args.RetainUntil, Valid: true},
		args.BypassGovernance,
	)
	if err != nil {
		// Two shapes mean the same refusal. The conditional upsert returns no
		// row when its WHERE declines the write; the BEFORE UPDATE trigger
		// (007) raises check_violation when something reaches the row without
		// going through that clause. The clause exists for the clean error,
		// the trigger for the guarantee — callers should not have to know
		// which one caught them.
		if errors.Is(err, pgx.ErrNoRows) || isLockWeakeningViolation(err) {
			return object.ObjectLock{}, object.ErrRetentionWeakened
		}
		return object.ObjectLock{}, fmt.Errorf("set object retention: %w", err)
	}
	return lockFrom(row.Mode, row.RetainUntil, row.LegalHold), nil
}

func (r *ObjectLockRepo) SetLegalHold(ctx context.Context, tenantID, versionID uuid.UUID, hold bool) (object.ObjectLock, error) {
	row, err := r.q.SetObjectLegalHold(ctx, pgUUID(tenantID), pgUUID(versionID), hold)
	if err != nil {
		return object.ObjectLock{}, fmt.Errorf("set legal hold: %w", err)
	}
	return lockFrom(row.Mode, row.RetainUntil, row.LegalHold), nil
}

// isLockWeakeningViolation identifies the retention trigger's refusal. The
// trigger raises check_violation, which is also what an ordinary CHECK raises,
// so the message is part of the test — a constraint failure from some other
// cause must not be reported to the caller as "retention cannot be weakened".
func isLockWeakeningViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		return false
	}
	return strings.Contains(pgErr.Message, "retained under") ||
		strings.Contains(pgErr.Message, "cannot be downgraded")
}

func (r *ObjectLockRepo) GetByVersion(ctx context.Context, versionID uuid.UUID) (object.ObjectLock, error) {
	row, err := r.q.GetObjectLockByVersion(ctx, pgUUID(versionID))
	if err != nil {
		// A version with no lock is unlocked, not missing. Returning an error
		// here would make every caller special-case the common case.
		if isNoRows(err) {
			return object.ObjectLock{}, nil
		}
		return object.ObjectLock{}, fmt.Errorf("get object lock: %w", err)
	}
	return lockFrom(row.Mode, row.RetainUntil, row.LegalHold), nil
}

func (r *ObjectLockRepo) ApplyBucketDefault(ctx context.Context, tenantID, versionID uuid.UUID, mode string, retention time.Duration) error {
	if mode == "" || retention <= 0 {
		return nil // no default configured; nothing to apply
	}
	if err := r.q.ApplyBucketDefaultLock(ctx,
		pgUUID(tenantID), pgUUID(versionID),
		sqlc.ObjectLockMode(mode), int64(retention.Seconds()),
	); err != nil {
		return fmt.Errorf("apply bucket default lock: %w", err)
	}
	return nil
}

func lockFrom(mode sqlc.NullObjectLockMode, retainUntil pgtype.Timestamptz, legalHold bool) object.ObjectLock {
	return object.ObjectLock{
		Mode:        lockModeFromSQL(mode),
		RetainUntil: timePtr(retainUntil),
		LegalHold:   legalHold,
	}
}
