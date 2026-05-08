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

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

type AuditRepoV2 struct {
	q *sqlc.Queries
}

func NewAuditRepoV2(q *sqlc.Queries) *AuditRepoV2 { return &AuditRepoV2{q: q} }

var _ admindomain.AuditRepository = (*AuditRepoV2)(nil)

func (r *AuditRepoV2) Insert(ctx context.Context, e admindomain.AuditEntry) error {
	if e.EntryID == uuid.Nil {
		e.EntryID = uuid.Must(uuid.NewV7())
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	return r.q.InsertAuditEntry(ctx,
		pgUUID(e.EntryID),
		pgTS(e.At),
		e.ActorSubject,
		pgUUIDOptional(e.ActorTenantID),
		e.ActorAudience,
		e.Action,
		e.ResourceName,
		strPtr(e.RequestID),
		strPtr(e.SourceIP),
		e.BeforeJSON,
		e.AfterJSON,
		strPtr(e.ErrorMessage),
		// CapabilityID is uuid.Nil when no capability was presented;
		// pgUUIDOptional maps that to a NULL DB value so the partial
		// index on the column stays small.
		pgUUIDOptional(e.CapabilityID),
	)
}

func (r *AuditRepoV2) Get(ctx context.Context, entryID uuid.UUID) (admindomain.AuditEntry, error) {
	row, err := r.q.GetAuditEntry(ctx, pgUUID(entryID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.AuditEntry{}, admindomain.ErrNotFound
		}
		return admindomain.AuditEntry{}, err
	}
	return auditEntryFromModel(row), nil
}

func (r *AuditRepoV2) List(ctx context.Context, args admindomain.ListAuditArgs) ([]admindomain.AuditEntry, string, error) {
	pageSize := args.PageSize
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 50
	}

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
		out = append(out, auditEntryFromModel(row))
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		last := out[len(out)-1]
		next = last.At.UTC().Format(time.RFC3339Nano) + "/" + last.EntryID.String()
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
		EntryID:       uuidFrom(row.EntryID),
		At:            timeFrom(row.At),
		ActorSubject:  row.ActorSubject,
		ActorTenantID: uuidFrom(row.ActorTenantID),
		ActorAudience: row.ActorAudience,
		Action:        row.Action,
		ResourceName:  row.ResourceName,
		RequestID:     derefStr(row.RequestID),
		SourceIP:      derefStr(row.SourceIp),
		BeforeJSON:    row.BeforeJson,
		AfterJSON:     row.AfterJson,
		ErrorMessage:  derefStr(row.ErrorMessage),
		CapabilityID:  uuidFrom(row.CapabilityID),
	}
}
