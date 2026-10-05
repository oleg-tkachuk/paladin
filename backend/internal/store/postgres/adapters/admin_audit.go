package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

type AuditRepoV2 struct {
	q *sqlc.Queries
	// pool backs InsertWithOutbox's transaction. Optional: nil on the
	// purger/bootstrap construction sites that only ever call Insert /
	// PurgeOlderThan (autocommit paths). InsertWithOutbox requires it.
	pool *pgxpool.Pool
}

// NewAuditRepoV2 wires the sqlc query set and (optionally) the pool that
// InsertWithOutbox opens its transaction on. Pass the pool on the
// request-serving construction site (the audit interceptor's writer);
// pool may be nil where only Insert/Get/List/Purge are used.
func NewAuditRepoV2(q *sqlc.Queries, pool *pgxpool.Pool) *AuditRepoV2 {
	return &AuditRepoV2{q: q, pool: pool}
}

var _ admindomain.AuditRepository = (*AuditRepoV2)(nil)

func (r *AuditRepoV2) Insert(ctx context.Context, e admindomain.AuditEntry) error {
	return r.insertWith(ctx, r.q, e)
}

// insertWith runs the audit INSERT against an arbitrary query set — the
// pool-backed r.q for autocommit (Insert) or a tx-bound q.WithTx(tx) for
// InsertWithOutbox. Fills the EntryID / At defaults in one place.
func (r *AuditRepoV2) insertWith(ctx context.Context, q *sqlc.Queries, e admindomain.AuditEntry) error {
	if e.EntryID == uuid.Nil {
		e.EntryID = uuid.Must(uuid.NewV7())
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	return q.InsertAuditEntry(ctx,
		pgUUID(e.EntryID),
		pgTS(e.At),
		e.ActorSubject,
		pgUUIDOptional(e.ActorTenantID),
		e.ActorAudience,
		e.Action,
		e.ResourceName,
		e.RequestID,
		strPtr(e.SourceIP),
		e.BeforeJSON,
		e.AfterJSON,
		strPtr(e.ErrorMessage),
		// CapabilityID is uuid.Nil when no capability was presented;
		// pgUUIDOptional maps that to a NULL DB value so the partial
		// index on the column stays small.
		pgUUIDOptional(e.CapabilityID),
		pgUUIDOptional(e.ResourceTenant()),
	)
}

// InsertWithOutbox inserts the audit entry and, when onInserted is
// non-nil, runs it inside the SAME transaction before commit — the
// event producer enqueues its fan-out outbox rows on `tx`, so the audit
// row and its mirror event commit atomically (ADR-0003 transactional
// outbox — no dual-write window). An error from onInserted rolls the
// audit row back too.
//
// onInserted == nil short-circuits to the plain autocommit Insert, so
// the default (no-mirror) path keeps its single-statement cost and needs
// no pool. When onInserted is non-nil, pool must be wired.
func (r *AuditRepoV2) InsertWithOutbox(
	ctx context.Context,
	e admindomain.AuditEntry,
	onInserted func(ctx context.Context, tx pgx.Tx) error,
) error {
	if onInserted == nil {
		return r.Insert(ctx, e)
	}
	if r.pool == nil {
		return errors.New("adapters: AuditRepoV2.InsertWithOutbox requires a pool")
	}
	if e.EntryID == uuid.Nil {
		e.EntryID = uuid.Must(uuid.NewV7())
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("audit insert begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit
	if err := r.insertWith(ctx, r.q.WithTx(tx), e); err != nil {
		return err
	}
	if err := onInserted(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *AuditRepoV2) Get(ctx context.Context, entryID uuid.UUID) (admindomain.AuditEntry, error) {
	row, err := r.q.GetAuditEntry(ctx, pgUUID(entryID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.AuditEntry{}, admindomain.ErrNotFound
		}
		return admindomain.AuditEntry{}, err
	}
	return auditEntryFromModel(row.AuditLog), nil
}

func (r *AuditRepoV2) List(ctx context.Context, args admindomain.ListAuditArgs) ([]admindomain.AuditEntry, string, error) {
	pageSize := pageSizeOrDefault(args.PageSize)

	// Optional predicates ride the canonical sqlc OR-NULL idiom: pass
	// nil / zero-valued pgtype to opt out, the query's
	// `(sqlc.narg(x) IS NULL OR …)` branch becomes a constant TRUE
	// at plan time. Postgres folds the disabled branches away — the
	// cost of unused predicates is one comparison-against-NULL each,
	// which the planner handles inline.
	//
	// CEL pushdown (audith.applyAuditPushdown) populates ActionEq /
	// ActionPrefix / AtGTE / AtLTE; the in-memory CEL pass after the
	// fetch is still authoritative for correctness.
	rows, err := r.q.ListAuditEntries(ctx,
		strPtrNonEmpty(args.ActorSubject),
		pgUUIDOptional(args.ActorTenantID),
		pgUUIDOptional(args.TrailTenantID),
		strPtrNonEmpty(args.ActionEq),
		likePrefixOrNil(args.ActionPrefix),
		tsOptional(args.AtGTE),
		tsOptional(args.AtLTE),
		tsOptional(args.AfterAt),
		pgUUID(args.AfterID),
		pageSize,
	)
	if err != nil {
		return nil, "", fmt.Errorf("list audit entries: %w", err)
	}
	out := make([]admindomain.AuditEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, auditEntryFromModel(row.AuditLog))
	}
	var next string
	if len(out) == int(pageSize) && len(out) > 0 {
		next = admindomain.AuditCursor(out[len(out)-1])
	}
	return out, next, nil
}

// strPtrNonEmpty returns nil for empty strings so the sqlc OR-NULL
// branch short-circuits at plan time. (strPtr always returns a non-
// nil pointer — that would force the predicate to match the empty
// string instead of being skipped.)
func strPtrNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

// likePrefixOrNil returns nil for empty prefix; otherwise returns a
// pointer to the LIKE pattern with caller-supplied prefix metachars
// escaped (default '\' escape character). Keeps the SQL predicate a
// pure prefix match — a stray `_` in the prefix won't widen the
// match.
func likePrefixOrNil(prefix string) *string {
	if prefix == "" {
		return nil
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	pat := r.Replace(prefix) + "%"
	return &pat
}

// tsOptional returns the zero-value pgtype.Timestamptz (Valid=false)
// when t.IsZero(), so the sqlc OR-NULL branch short-circuits.
func tsOptional(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgTS(t)
}

// PurgeOlderThan removes audit_log rows whose `at` is before cutoff. Used by
// the AuditLogPurger worker.
func (r *AuditRepoV2) PurgeOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	return r.q.PurgeAuditOlderThan(ctx, pgTS(cutoff))
}

func auditEntryFromModel(row sqlc.AuditLog) admindomain.AuditEntry {
	return admindomain.AuditEntry{
		EntryID:       uuidFrom(row.ID),
		At:            timeFrom(row.At),
		ActorSubject:  row.ActorSubject,
		ActorTenantID: uuidFrom(row.ActorTenantID),
		ActorAudience: row.ActorAudience,
		Action:        row.Action,
		ResourceName:  row.ResourceName,
		RequestID:     row.RequestID,
		SourceIP:      derefStr(row.SourceIp),
		BeforeJSON:    row.BeforeJson,
		AfterJSON:     row.AfterJson,
		ErrorMessage:  derefStr(row.ErrorMessage),
		CapabilityID:  uuidFrom(row.CapabilityID),

		ResourceTenantID: uuidFrom(row.ResourceTenantID),
	}
}
