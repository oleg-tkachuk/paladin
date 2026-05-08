package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

type AuditRepoV2 struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

// NewAuditRepoV2 builds the audit repo. pool is required for List —
// the CEL-pushdown predicates compose into a dynamic WHERE clause that
// sqlc cannot express statically. Insert / Get / Purge stay on sqlc.
func NewAuditRepoV2(q *sqlc.Queries, pool *pgxpool.Pool) *AuditRepoV2 {
	return &AuditRepoV2{q: q, pool: pool}
}

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

	// Compose dynamic WHERE. Each clause appends a placeholder
	// reference and pushes the value onto a single argv slice; clauses
	// are joined with AND. Order of placeholders matches the order
	// fragments are appended.
	var (
		clauses []string
		argv    []any
	)
	add := func(frag string, val any) {
		argv = append(argv, val)
		clauses = append(clauses, fmt.Sprintf(frag, len(argv)))
	}
	if args.ActorSubject != "" {
		add("actor_subject = $%d", args.ActorSubject)
	}
	if args.ActorTenantID != uuid.Nil {
		add("actor_tenant_id = $%d", args.ActorTenantID)
	}
	if args.ActionEq != "" {
		add("action = $%d", args.ActionEq)
	}
	if args.ActionPrefix != "" {
		// LIKE pattern: caller-supplied prefix + '%'. Escape the LIKE
		// metachars in the prefix so a stray underscore doesn't widen
		// the match unexpectedly.
		add("action LIKE $%d", escapeLikePrefix(args.ActionPrefix)+"%")
	}
	if !args.AtGTE.IsZero() {
		add("at >= $%d", args.AtGTE)
	}
	if !args.AtLTE.IsZero() {
		add("at <= $%d", args.AtLTE)
	}
	// Cursor predicate is a tuple inequality on (at, entry_id).
	if !args.AfterAt.IsZero() {
		argv = append(argv, args.AfterAt, args.AfterID)
		clauses = append(clauses, fmt.Sprintf(
			"(at < $%d OR (at = $%[1]d AND entry_id < $%d))",
			len(argv)-1, len(argv),
		))
	}

	where := "TRUE"
	if len(clauses) > 0 {
		where = strings.Join(clauses, " AND ")
	}
	argv = append(argv, pageSize)
	q := fmt.Sprintf(`
		SELECT entry_id, at, actor_subject, actor_tenant_id, actor_audience,
		       action, resource_name, request_id, source_ip,
		       before_json, after_json, error_message, capability_id
		  FROM audit_log
		 WHERE %s
		 ORDER BY at DESC, entry_id DESC
		 LIMIT $%d`, where, len(argv))

	rows, err := r.pool.Query(ctx, q, argv...)
	if err != nil {
		return nil, "", fmt.Errorf("list audit entries: %w", err)
	}
	defer rows.Close()
	out := make([]admindomain.AuditEntry, 0, pageSize)
	for rows.Next() {
		var m sqlc.AuditLog
		if err := rows.Scan(
			&m.EntryID, &m.At, &m.ActorSubject, &m.ActorTenantID, &m.ActorAudience,
			&m.Action, &m.ResourceName, &m.RequestID, &m.SourceIp,
			&m.BeforeJson, &m.AfterJson, &m.ErrorMessage, &m.CapabilityID,
		); err != nil {
			return nil, "", fmt.Errorf("list audit entries: scan: %w", err)
		}
		out = append(out, auditEntryFromModel(m))
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list audit entries: %w", err)
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		last := out[len(out)-1]
		next = last.At.UTC().Format(time.RFC3339Nano) + "/" + last.EntryID.String()
	}
	return out, next, nil
}

// escapeLikePrefix backslash-escapes the LIKE metacharacters in a
// caller-supplied prefix so `action LIKE $1` only matches the literal
// prefix. Postgres treats backslash as escape only when ESCAPE is
// supplied; we rely on the default '\' escape character.
func escapeLikePrefix(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(p)
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
