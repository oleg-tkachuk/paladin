// Package postgres is the Store implementation for capability records,
// backed by the schema baseline (001_initial_schema.sql). SQL is hand-written rather than sqlc-generated
// because the surface is small (six methods) and the JSON-encoded claim
// payload is awkward through sqlc's typed-mapping path. Schema lives at
// migrations/001_initial_schema.sql.
//
// Connection ownership: Store does not Close the pool — the caller
// (cmd/server boot path) owns the *pgxpool.Pool lifecycle. The hot-path
// methods (IsRevoked, Get) issue exactly one round-trip; admin methods
// (Revoke with cascade) issue one transaction.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/capability"
)

// ErrNotFound re-exports the core sentinel so existing call sites keep
// compiling. The value is deliberately capability.ErrNotFound, not a distinct
// error: the Store contract is owned by the core package (FR-004), and a
// second sentinel here would mean a third-party Store implementation and this
// one disagree about what "not found" is.
var ErrNotFound = capability.ErrNotFound

// Store implements capability.Store against the capability tables in
// `001_initial_schema.sql`.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs a Store. Caller owns pool lifecycle.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("capability/postgres: pool required")
	}
	return &Store{pool: pool}, nil
}

// tenantForeignKey is capability_records' reference to its tenant, as
// 001_initial_schema.sql names it.
const tenantForeignKey = "capability_records_tenant_id_fkey"

// lockLiveTenant refuses a capability for a tenant that does not exist or is
// in the trash, and holds the tenant's row until the insert commits, so a
// concurrent delete cannot land between the check and the insert. The
// foreign key alone would admit a trashed tenant, whose row still exists.
func lockLiveTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	var deletedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT deleted_at FROM tenants WHERE id = $1 FOR SHARE`, tenantID).Scan(&deletedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: %s", capability.ErrUnknownTenant, tenantID)
	case err != nil:
		return fmt.Errorf("capability/postgres: lock tenant: %w", err)
	case deletedAt != nil:
		return fmt.Errorf("%w: %s", capability.ErrTenantDeleted, tenantID)
	}
	return nil
}

// Insert implements capability.Store.
//
// RLS handling: the RLS baseline (002_roles_and_rls.sql) enables row-level security on
// capability_records keyed on the session GUC `paladin.tenant_id`. The
// pool's PrepareConn hook (see internal/store/postgres/rls.go) sets
// that GUC from the request's JWT — which is correct for in-tenant
// flows but wrong when a platform-admin issues a capability for a
// DIFFERENT tenant (the JWT carries the platform tenant, the row
// carries the target tenant, RLS rejects with 42501).
//
// Fix: wrap the INSERT in a transaction that `SET LOCAL paladin.tenant_id`
// to the row's tenant_id. This is safe for every caller because the
// GUC value matches the row being inserted by construction. Mirrors
// the `SET LOCAL paladin.bypass_governance_retention = 'on'` pattern used in the
// object hard-delete adapter (see store/postgres/adapters/object.go).
//
// The SET LOCAL scope dies with the transaction, so the connection's
// pool-level GUC (set by PrepareConn) is restored automatically on
// release without an explicit reset.
func (s *Store) Insert(ctx context.Context, c capability.Capability, issuedBy capability.Principal) error {
	principalPayload, err := json.Marshal(c.Subject)
	if err != nil {
		return fmt.Errorf("capability/postgres: marshal principal: %w", err)
	}
	caveats, err := json.Marshal(c.Caveats)
	if err != nil {
		return fmt.Errorf("capability/postgres: marshal caveats: %w", err)
	}

	const stmt = `
INSERT INTO capability_records (
    id, tenant_id, issuer, principal_kind, principal_subject,
    principal_payload, audience, caveats, parent_id, generation,
    issued_at, not_before, expires_at, created_by, confirmation_jkt
) VALUES (
    $1, $2, $3, $4, $5,
    $6::jsonb, $7, $8::jsonb, $9, $10,
    $11, $12, $13, $14, NULLIF($15, '')
);
`
	var parent *uuid.UUID
	if c.ParentID != uuid.Nil {
		parent = &c.ParentID
	}
	var nbf *time.Time
	if !c.NotBefore.IsZero() {
		nbf = &c.NotBefore
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("capability/postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Align the session GUC with the row's tenant_id so the
	// capability_records RLS policy (the RLS baseline (002_roles_and_rls.sql)) admits the
	// INSERT regardless of the caller's JWT tenant. See the doc
	// comment above for the cross-tenant platform-admin case.
	if _, err := tx.Exec(
		ctx,
		`SELECT set_config('paladin.tenant_id', $1, true)`,
		c.Subject.TenantID.String(),
	); err != nil {
		return fmt.Errorf("capability/postgres: set tenant GUC: %w", err)
	}
	if err := lockLiveTenant(ctx, tx, c.Subject.TenantID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, stmt,
		c.ID,
		c.Subject.TenantID,
		c.Issuer,
		string(c.Subject.Type),
		c.Subject.Subject,
		principalPayload,
		c.Audience,
		caveats,
		parent,
		c.Generation,
		c.IssuedAt,
		nbf,
		c.ExpiresAt,
		// The principal that requested the issuance, passed down the Store
		// contract rather than read from a request context — the capability
		// module still depends on no auth pipeline.
		issuedBy.Subject,
		c.ConfirmationJKT,
	); err != nil {
		// The lock above makes this unreachable short of a tenant removed by
		// something that ignores it; the constraint is the last word either
		// way, and says nothing about the request.
		if pgerr.Is(err, pgerr.ForeignKeyViolation) && pgerr.Constraint(err) == tenantForeignKey {
			return fmt.Errorf("%w: %s", capability.ErrUnknownTenant, c.Subject.TenantID)
		}
		return fmt.Errorf("capability/postgres: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("capability/postgres: commit: %w", err)
	}
	return nil
}

// Get implements capability.Store.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (*capability.Capability, error) {
	const stmt = `
SELECT id, tenant_id, issuer, principal_kind, principal_subject,
       principal_payload, audience, caveats, parent_id, generation,
       issued_at, not_before, expires_at, COALESCE(confirmation_jkt, '')
FROM   capability_records
WHERE  id = $1;
`
	row := s.pool.QueryRow(ctx, stmt, id)
	return scanRow(row)
}

// IsRevoked implements capability.Store: true when the capability or any
// ancestor still on record is revoked. One round trip — a recursive walk up
// parent_id joined against the revocation list — and verifiers wrap it in an
// in-memory TTL cache so the per-request cost stays flat.
//
// The verifier calls this before any tenant is on the context: the tenant is
// inside the token still being checked, so the pool's PrepareConn hook binds
// an empty paladin.tenant_id. capability_revocations is isolated through the
// capability each row points at, so on that connection every revocation was
// invisible and a revoked capability verified as live. The read therefore
// runs under the cross-tenant flag, SET LOCAL to this transaction. The policy
// admits that flag in USING only, so it widens this lookup and cannot let
// anything write.
func (s *Store) IsRevoked(ctx context.Context, id uuid.UUID) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, fmt.Errorf("capability/postgres: is_revoked begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // read-only: nothing to commit

	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.cross_tenant', 'on', true)`); err != nil {
		return false, fmt.Errorf("capability/postgres: is_revoked set cross-tenant: %w", err)
	}
	const stmt = `
WITH RECURSIVE chain(id, parent_id, depth) AS (
    SELECT id, parent_id, 0 FROM capability_records WHERE id = $1
    UNION ALL
    SELECT r.id, r.parent_id, c.depth + 1
    FROM   capability_records r
    JOIN   chain c ON r.id = c.parent_id
    WHERE  c.depth < 64
)
SELECT EXISTS (
    SELECT 1 FROM capability_revocations v
    WHERE  v.id = $1 OR v.id IN (SELECT id FROM chain)
);
`
	var revoked bool
	if err := tx.QueryRow(ctx, stmt, id).Scan(&revoked); err != nil {
		return false, fmt.Errorf("capability/postgres: is_revoked: %w", err)
	}
	return revoked, nil
}

// Revoke implements capability.Store. Descendants stop verifying either way,
// because IsRevoked walks the chain; CascadeChildren additionally walks the
// delegation tree via a recursive CTE and inserts a revocation row for every
// descendant in one transaction, so the audit trail names each of them.
//
// Both paths source their ids from capability_records rather than trusting
// the argument, so a caller that cannot see the capability cannot revoke it.
// That mattered because capability_revocations carries no tenant_id of its
// own: before 004 the table had no policy at all, and an INSERT keyed on a
// caller-supplied uuid let one tenant deny another's credentials. The SELECT
// is the boundary; the RLS policy added in 004 is the backstop.
//
// A capability the caller cannot see is ErrNotFound, not a silent success —
// otherwise the admin RPC answers "revoked" for an id that was never touched.
// Revoking an already-revoked capability stays a no-op, because the row's
// visibility, not the INSERT's row count, is what the result is read from.
//
// The recursive CTE bounds depth via WHERE NOT in the cycle — capability
// records are a forest (parent_id is nullable, no cycles by construction
// because the FK is set NULL on parent delete), but a depth limit is
// kept as a defensive guard against pathological dataset corruption.
func (s *Store) Revoke(ctx context.Context, args capability.RevokeRequest) error {
	if args.ID == uuid.Nil {
		return errors.New("capability/postgres: revoke ID required")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("capability/postgres: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if args.CascadeChildren {
		const cascade = `
WITH RECURSIVE descendants(id, depth) AS (
    SELECT id, 0 FROM capability_records WHERE id = $1
    UNION ALL
    SELECT r.id, d.depth + 1
    FROM   capability_records r
    JOIN   descendants d ON r.parent_id = d.id
    WHERE  d.depth < 64
),
inserted AS (
    INSERT INTO capability_revocations (id, reason, actor, cascade)
    SELECT id, $2, $3, true FROM descendants
    ON CONFLICT (id) DO NOTHING
    RETURNING 1
)
SELECT count(*) FROM descendants;
`
		var visible int64
		if err := tx.QueryRow(ctx, cascade, args.ID, args.Reason, args.Actor).Scan(&visible); err != nil {
			return fmt.Errorf("capability/postgres: revoke cascade: %w", err)
		}
		if visible == 0 {
			return ErrNotFound
		}
	} else {
		const single = `
WITH target AS (
    SELECT id FROM capability_records WHERE id = $1
),
inserted AS (
    INSERT INTO capability_revocations (id, reason, actor, cascade)
    SELECT id, $2, $3, false FROM target
    ON CONFLICT (id) DO NOTHING
    RETURNING 1
)
SELECT count(*) FROM target;
`
		var visible int64
		if err := tx.QueryRow(ctx, single, args.ID, args.Reason, args.Actor).Scan(&visible); err != nil {
			return fmt.Errorf("capability/postgres: revoke: %w", err)
		}
		if visible == 0 {
			return ErrNotFound
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("capability/postgres: commit: %w", err)
	}
	return nil
}

// PurgeExpired implements capability.Store. Drops revocation rows — of
// capabilities and of Biscuit copies — whose underlying capability has been
// expired for at least the supplied grace; keeps the denylists bounded over
// time. Returns the rows dropped from both.
func (s *Store) PurgeExpired(ctx context.Context, expiredFor time.Duration) (int64, error) {
	const stmt = `
DELETE FROM capability_revocations
WHERE id IN (
    SELECT r.id
    FROM   capability_revocations r
    JOIN   capability_records cr ON cr.id = r.id
    WHERE  cr.expires_at < NOW() - ($1::bigint || ' microseconds')::interval
);
`
	tag, err := s.pool.Exec(ctx, stmt, expiredFor.Microseconds())
	if err != nil {
		return 0, fmt.Errorf("capability/postgres: purge: %w", err)
	}
	// Revoked Biscuit copies go on the same grace: no copy outlives its
	// capability's expiry.
	const copies = `
DELETE FROM capability_biscuit_revocations
WHERE capability_id IN (
    SELECT id FROM capability_records
    WHERE  expires_at < NOW() - ($1::bigint || ' microseconds')::interval
);
`
	copyTag, err := s.pool.Exec(ctx, copies, expiredFor.Microseconds())
	if err != nil {
		return 0, fmt.Errorf("capability/postgres: purge biscuit copies: %w", err)
	}
	return tag.RowsAffected() + copyTag.RowsAffected(), nil
}

// ListByPrincipal implements capability.Store. Cursor is the last seen
// id encoded as a hex string; a follow-up page seeks past it. Page size
// is bounded by Limit (default 50, max 500).
func (s *Store) ListByPrincipal(ctx context.Context, args capability.ListByPrincipalRequest) ([]capability.Capability, string, error) {
	if args.TenantID == uuid.Nil {
		return nil, "", errors.New("capability/postgres: tenant_id required")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	// Build clauses incrementally so the query plan stays readable.
	whereExtra := ""
	bindArgs := []any{args.TenantID, string(args.PrincipalType), args.Subject}
	if !args.IncludeExpired {
		whereExtra += " AND cr.expires_at > NOW()"
	}
	if !args.IncludeRevoked {
		whereExtra += " AND NOT EXISTS (SELECT 1 FROM capability_revocations rv WHERE rv.id = cr.id)"
	}
	if args.Cursor != "" {
		bindArgs = append(bindArgs, args.Cursor)
		whereExtra += fmt.Sprintf(" AND cr.id > $%d::uuid", len(bindArgs))
	}
	bindArgs = append(bindArgs, limit+1) // fetch +1 to detect next page
	stmt := fmt.Sprintf(`
SELECT id, tenant_id, issuer, principal_kind, principal_subject,
       principal_payload, audience, caveats, parent_id, generation,
       issued_at, not_before, expires_at, COALESCE(confirmation_jkt, '')
FROM   capability_records cr
WHERE  cr.tenant_id = $1
  AND  cr.principal_kind = $2
  AND  cr.principal_subject = $3
  %s
ORDER  BY cr.id
LIMIT  $%d;
`, whereExtra, len(bindArgs))

	rows, err := s.pool.Query(ctx, stmt, bindArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("capability/postgres: list: %w", err)
	}
	defer rows.Close()

	out := make([]capability.Capability, 0, limit)
	for rows.Next() {
		c, err := scanRow(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("capability/postgres: list scan: %w", err)
	}

	nextCursor := ""
	if len(out) > int(limit) {
		out = out[:limit]
		// Seek past the last row RETURNED, not past the overflow row we
		// fetched to detect the next page — the overflow row belongs to
		// the next page and seeking past it drops it entirely.
		nextCursor = out[len(out)-1].ID.String()
	}
	return out, nextCursor, nil
}

// scanRow is the shared row decoder for Get / ListByPrincipal. Accepts
// any pgx Scanner so it works against single-row and multi-row results.
type scanner interface {
	Scan(dest ...any) error
}

func scanRow(r scanner) (*capability.Capability, error) {
	var (
		id, tenantID          uuid.UUID
		issuer, kind, subject string
		principalRaw, caveats []byte
		audience              []string
		parent                *uuid.UUID
		generation            int64
		issuedAt              time.Time
		notBefore             *time.Time
		expiresAt             time.Time
		confirmationJKT       string
	)
	err := r.Scan(
		&id, &tenantID, &issuer, &kind, &subject,
		&principalRaw, &audience, &caveats, &parent, &generation,
		&issuedAt, &notBefore, &expiresAt, &confirmationJKT,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("capability/postgres: scan: %w", err)
	}

	var principal capability.Principal
	if err := json.Unmarshal(principalRaw, &principal); err != nil {
		return nil, fmt.Errorf("capability/postgres: parse principal: %w", err)
	}
	// Defensive: principal payload sometimes lacks tenant_id when it was
	// written by a buggy issuer. Fall back to the column we indexed on.
	if principal.TenantID == uuid.Nil {
		principal.TenantID = tenantID
	}
	if principal.Type == "" {
		principal.Type = capability.PrincipalType(kind)
	}
	if principal.Subject == "" {
		principal.Subject = subject
	}

	var cav capability.Caveats
	if err := json.Unmarshal(caveats, &cav); err != nil {
		return nil, fmt.Errorf("capability/postgres: parse caveats: %w", err)
	}

	out := &capability.Capability{
		ID:              id,
		Issuer:          issuer,
		Subject:         principal,
		Audience:        audience,
		Caveats:         cav,
		IssuedAt:        issuedAt.UTC(),
		ExpiresAt:       expiresAt.UTC(),
		Generation:      generation,
		ConfirmationJKT: confirmationJKT,
	}
	if parent != nil {
		out.ParentID = *parent
	}
	if notBefore != nil {
		out.NotBefore = notBefore.UTC()
	}
	return out, nil
}

// Compile-time conformance. The relational implementation is the reference,
// not part of the module's contract (FR-015) — so drift between it and the
// published interface must fail the build here rather than surface as a
// runtime error on an admin RPC.
var _ capability.Store = (*Store)(nil)
