// Package postgres is the Store implementation for capability records,
// backed by the schema baseline (001_initial_schema.sql). SQL is hand-written rather than sqlc-generated
// because the surface is small (six methods) and the JSON-encoded claim
// payload is awkward through sqlc's typed-mapping path. Schema lives at
// migrations/001_initial_schema.sql.
//
// Connection ownership: Store does not Close the pool — the caller
// (cmd/server boot path) owns the *pgxpool.Pool lifecycle. The hot-path
// methods (IsRevoked, GetRecord) issue exactly one round-trip; admin methods
// (Revoke with cascade) issue one transaction.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgmoney"
)

// ErrNotFound re-exports the core sentinel so existing call sites keep
// compiling. The value is deliberately limes.ErrNotFound, not a distinct
// error: the Store contract is owned by the core package (FR-004), and a
// second sentinel here would mean a third-party Store implementation and this
// one disagree about what "not found" is.
var ErrNotFound = limes.ErrNotFound

// Store implements limes.Store against the capability tables in
// `001_initial_schema.sql`.
type Store struct {
	pool *pgxpool.Pool
	// now is the clock expiry is measured by: when a capability stops
	// listing, when it is due for Purge, and when a revocation was written.
	now func() time.Time
}

// Option configures a Store.
type Option func(*Store)

// WithClock measures expiry by now instead of the wall clock. For tests that
// move time; the server runs on the wall clock.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// New constructs a Store. Caller owns pool lifecycle.
func New(pool *pgxpool.Pool, opts ...Option) (*Store, error) {
	if pool == nil {
		return nil, errors.New("capability/postgres: pool required")
	}
	s := &Store{pool: pool, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	if s.now == nil {
		return nil, errors.New("capability/postgres: clock required")
	}
	return s, nil
}

// tenantForeignKey is capability_records' reference to its tenant, as
// 001_initial_schema.sql names it.
const tenantForeignKey = "capability_records_tenant_id_fkey"

// recordsPrimaryKey is capability_records' primary key, as Postgres names it
// by default for 001_initial_schema.sql.
const recordsPrimaryKey = "capability_records_pkey"

// lockLiveTenant refuses a capability for a tenant that does not exist or is
// in the trash, and holds the tenant's row until the insert commits, so a
// concurrent delete cannot land between the check and the insert. The
// foreign key alone would admit a trashed tenant, whose row still exists.
func lockLiveTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	var deletedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT deleted_at FROM tenants WHERE id = $1 FOR SHARE`, tenantID).Scan(&deletedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: %s", limes.ErrUnknownTenant, tenantID)
	case err != nil:
		return fmt.Errorf("capability/postgres: lock tenant: %w", err)
	case deletedAt != nil:
		return fmt.Errorf("%w: %s", limes.ErrTenantDeleted, tenantID)
	}
	return nil
}

// Insert implements limes.Store.
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
func (s *Store) Insert(ctx context.Context, req limes.InsertRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	c, issuedBy := req.Capability, req.IssuedBy
	principalPayload, err := marshalPrincipal(c.Subject)
	if err != nil {
		return err
	}
	issuedByPayload, err := marshalPrincipal(issuedBy)
	if err != nil {
		return err
	}
	caveats, err := marshalCaveats(c.Caveats)
	if err != nil {
		return err
	}

	const stmt = `
INSERT INTO capability_records (
    id, tenant_id, issuer, principal_kind, principal_subject,
    principal_payload, audience, caveats, parent_id, generation,
    issued_at, not_before, expires_at, created_by, confirmation_jkt,
    issued_by, max_budget, max_requests
) VALUES (
    $1, $2, $3, $4, $5,
    $6::jsonb, $7, $8::jsonb, $9, $10,
    $11, $12, $13, $14, NULLIF($15, ''),
    $16::jsonb, $17::numeric, $18
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
	if err := checkLineage(ctx, tx, c); err != nil {
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
		issuedByPayload,
		// The ceilings again, in columns: the charge path reads an
		// ancestor's from here, not from the caveats JSON. NULL lifts one.
		pgmoney.NumericFromCeiling(c.Caveats.Budget),
		requestCeilingColumn(c.Caveats.Requests),
	); err != nil {
		// The lock above makes this unreachable short of a tenant removed by
		// something that ignores it; the constraint is the last word either
		// way, and says nothing about the request.
		if pgerr.Is(err, pgerr.ForeignKeyViolation) && pgerr.Constraint(err) == tenantForeignKey {
			return fmt.Errorf("%w: %s", limes.ErrUnknownTenant, c.Subject.TenantID)
		}
		if pgerr.Is(err, pgerr.UniqueViolation) && pgerr.Constraint(err) == recordsPrimaryKey {
			return fmt.Errorf("%w: %s", limes.ErrAlreadyExists, c.ID)
		}
		return fmt.Errorf("capability/postgres: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("capability/postgres: commit: %w", err)
	}
	return nil
}

// parentChainQuery walks from a parent up its chain, one ancestor past
// MaxDelegationDepth ($2): the parent's tenant and expiry, and how many
// capabilities the chain holds — the ancestors a child under it would have.
const parentChainQuery = `
WITH RECURSIVE chain(id, parent_id, depth) AS (
    SELECT id, parent_id, 1 FROM capability_records WHERE id = $1
    UNION ALL
    SELECT r.id, r.parent_id, c.depth + 1
    FROM   capability_records r
    JOIN   chain c ON r.id = c.parent_id
    WHERE  c.depth <= $2
)
SELECT p.tenant_id, p.expires_at, (SELECT count(*) FROM chain)
FROM   capability_records p
WHERE  p.id = $1;
`

// clearCrossTenantQuery narrows the reads of the current transaction back to
// its own tenant after setCrossTenantQuery.
const clearCrossTenantQuery = `SELECT set_config('paladin.cross_tenant', '', true)`

// checkLineage refuses a child whose parent is not on record, one in another
// tenant than its parent, one that would outlive its parent, and one with more
// than MaxDelegationDepth ancestors: Purge takes the deepest records first and
// the IsRevoked walk stops at the first ancestor gone, which both hold only
// for a lineage that is whole, in one tenant, and expires from the leaves up.
// The walk reads across tenants — a parent of another tenant must show as
// one, not as missing — and the transaction is narrowed back to the child's
// tenant before the insert.
func checkLineage(ctx context.Context, tx pgx.Tx, c limes.Capability) error {
	if c.ParentID == uuid.Nil {
		return nil
	}
	if _, err := tx.Exec(ctx, setCrossTenantQuery); err != nil {
		return fmt.Errorf("capability/postgres: lineage set cross-tenant: %w", err)
	}
	var (
		parentTenant  uuid.UUID
		parentExpires time.Time
		ancestors     int
	)
	err := tx.QueryRow(ctx, parentChainQuery, c.ParentID, limes.MaxDelegationDepth).Scan(&parentTenant, &parentExpires, &ancestors)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: capability/postgres: parent %s is not on record", limes.ErrInvalidRequest, c.ParentID)
	case err != nil:
		return fmt.Errorf("capability/postgres: read parent: %w", err)
	case parentTenant != c.Subject.TenantID:
		return fmt.Errorf("%w: tenant %s, under a parent of tenant %s", limes.ErrDelegationTooWide, c.Subject.TenantID, parentTenant)
	case c.ExpiresAt.After(parentExpires):
		return fmt.Errorf("%w: expires %s, after its parent %s", limes.ErrDelegationTooWide, c.ExpiresAt, parentExpires)
	case ancestors > limes.MaxDelegationDepth:
		return fmt.Errorf("%w: %s would have more than %d ancestors", limes.ErrDelegationTooDeep, c.ID, limes.MaxDelegationDepth)
	}
	if _, err := tx.Exec(ctx, clearCrossTenantQuery); err != nil {
		return fmt.Errorf("capability/postgres: lineage clear cross-tenant: %w", err)
	}
	return nil
}

// requestCeilingColumn is a request ceiling as max_requests holds it: its
// Max, or NULL when it is Unlimited.
func requestCeilingColumn(c limes.RequestCeiling) *int64 {
	if c.Unlimited {
		return nil
	}
	return &c.Max
}

// getRecordQuery is scanRow's columns, then who issued the capability and its
// own revocation entry, if any. capability_revocations is visible through
// the capability it names (004), so the join sees what the record read sees.
const getRecordQuery = `
SELECT cr.id, cr.tenant_id, cr.issuer, cr.principal_kind, cr.principal_subject,
       cr.principal_payload, cr.audience, cr.caveats, cr.parent_id, cr.generation,
       cr.issued_at, cr.not_before, cr.expires_at, COALESCE(cr.confirmation_jkt, ''),
       cr.created_by, cr.issued_by,
       rv.revoked_at, COALESCE(rv.reason, ''), COALESCE(rv.actor, ''), COALESCE(rv.cascade, false)
FROM   capability_records cr
LEFT   JOIN capability_revocations rv ON rv.id = cr.id
WHERE  cr.id = $1;
`

// trailingScanner scans a row into scanRow's destinations, then into extra:
// a query that selects scanRow's columns first and more after them.
type trailingScanner struct {
	row   scanner
	extra []any
}

func (t trailingScanner) Scan(dest ...any) error { return t.row.Scan(append(dest, t.extra...)...) }

// GetRecord implements limes.Store.
func (s *Store) GetRecord(ctx context.Context, id uuid.UUID) (limes.Record, error) {
	var (
		createdBy   string
		issuedByRaw []byte
		revokedAt   *time.Time
		note, actor string
		cascaded    bool
	)
	c, err := scanRow(trailingScanner{
		row:   s.pool.QueryRow(ctx, getRecordQuery, id),
		extra: []any{&createdBy, &issuedByRaw, &revokedAt, &note, &actor, &cascaded},
	})
	if err != nil {
		return limes.Record{}, err
	}
	rec := limes.Record{Capability: *c, IssuedBy: limes.Principal{Subject: createdBy}}
	if issuedByRaw != nil {
		if rec.IssuedBy, err = unmarshalPrincipal(issuedByRaw); err != nil {
			return limes.Record{}, err
		}
	}
	if revokedAt != nil {
		rec.Revocation = &limes.Revocation{RevokedAt: revokedAt.UTC(), Note: note, Actor: actor, Cascaded: cascaded}
	}
	return rec, nil
}

// IsRevoked implements limes.Store: true when the capability or any
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
	// The walk goes one ancestor past MaxDelegationDepth: an ancestor on
	// record that far up means a chain deeper than the contract answers for,
	// and a walk stopped short of it must not report "not revoked".
	const stmt = `
WITH RECURSIVE chain(id, parent_id, depth) AS (
    SELECT id, parent_id, 0 FROM capability_records WHERE id = $1
    UNION ALL
    SELECT r.id, r.parent_id, c.depth + 1
    FROM   capability_records r
    JOIN   chain c ON r.id = c.parent_id
    WHERE  c.depth <= $2
)
SELECT EXISTS (
           SELECT 1 FROM capability_revocations v
           WHERE  v.id = $1 OR v.id IN (SELECT id FROM chain WHERE depth <= $2)
       ),
       EXISTS (SELECT 1 FROM chain WHERE depth > $2);
`
	var revoked, tooDeep bool
	if err := tx.QueryRow(ctx, stmt, id, limes.MaxDelegationDepth).Scan(&revoked, &tooDeep); err != nil {
		return false, fmt.Errorf("capability/postgres: is_revoked: %w", err)
	}
	if tooDeep {
		return false, fmt.Errorf("%w: %s has more than %d ancestors", limes.ErrDelegationTooDeep, id, limes.MaxDelegationDepth)
	}
	return revoked, nil
}

// Revoke implements limes.Store. Descendants stop verifying either way,
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
// The cascade walks down to MaxDelegationDepth, as far as IsRevoked walks
// up: capability records are a forest (parent_id is nullable, no cycles by
// construction because the FK is set NULL on parent delete), and the bound
// also guards against pathological dataset corruption.
func (s *Store) Revoke(ctx context.Context, args limes.RevokeRequest) error {
	if args.CapabilityID == uuid.Nil {
		return ErrNotFound // no capability has the nil id
	}
	revokedAt := s.now()

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
    WHERE  d.depth < $5
),
inserted AS (
    INSERT INTO capability_revocations (id, revoked_at, reason, actor, cascade)
    SELECT id, $4, $2, $3, true FROM descendants
    ON CONFLICT (id) DO NOTHING
    RETURNING 1
)
SELECT count(*) FROM descendants;
`
		var visible int64
		if err := tx.QueryRow(ctx, cascade, args.CapabilityID, args.Note, args.Actor, revokedAt,
			limes.MaxDelegationDepth).Scan(&visible); err != nil {
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
    INSERT INTO capability_revocations (id, revoked_at, reason, actor, cascade)
    SELECT id, $4, $2, $3, false FROM target
    ON CONFLICT (id) DO NOTHING
    RETURNING 1
)
SELECT count(*) FROM target;
`
		var visible int64
		if err := tx.QueryRow(ctx, single, args.CapabilityID, args.Note, args.Actor, revokedAt).Scan(&visible); err != nil {
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

// purgeQuery removes, at most $3 of them, the capability records expired
// before $1 - $2: oldest expiry first and, among records expiring together,
// the deepest first — depth being how many ancestors are still on record —
// so a batch cut at its limit takes a child before its parent. Insert refuses
// a child that outlives its parent, so a child is due no later than its
// parent and the IsRevoked walk never loses an ancestor a live record still
// has. The revocation entries of the record and of its Biscuit copies go with
// it, by their CASCADE; what the meter holds for it stays until
// UsageHousekeeping.PurgeOrphans (067).
const purgeQuery = `
WITH RECURSIVE due AS (
    SELECT id, parent_id, expires_at
    FROM   capability_records
    WHERE  expires_at < $1::timestamptz - ($2::bigint * interval '1 microsecond')
),
up(id, ancestor, depth) AS (
    SELECT id, parent_id, 0 FROM due
    UNION ALL
    SELECT u.id, r.parent_id, u.depth + 1
    FROM   up u
    JOIN   capability_records r ON r.id = u.ancestor
    WHERE  u.depth <= $4
),
picked AS (
    SELECT d.id
    FROM   due d
    JOIN   (SELECT id, max(depth) AS depth FROM up GROUP BY id) depths USING (id)
    ORDER  BY d.expires_at, depths.depth DESC, d.id
    LIMIT  $3
)
DELETE FROM capability_records WHERE id IN (SELECT id FROM picked);
`

// Purge implements limes.Store: one batch of purgeQuery.
func (s *Store) Purge(ctx context.Context, req limes.PurgeRequest) (limes.PurgeResult, error) {
	if err := req.Validate(); err != nil {
		return limes.PurgeResult{}, err
	}
	limit := req.Limit
	if limit == 0 {
		limit = limes.DefaultHousekeepingLimit
	}
	// Housekeeping is platform-wide: the batch reads and removes every
	// tenant's records, whatever tenant the caller's connection is scoped
	// to. The policies admit the flag in USING only, which is all a delete
	// needs; it cannot let anything write.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return limes.PurgeResult{}, fmt.Errorf("capability/postgres: purge begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.cross_tenant', 'on', true)`); err != nil {
		return limes.PurgeResult{}, fmt.Errorf("capability/postgres: purge scope: %w", err)
	}
	tag, err := tx.Exec(ctx, purgeQuery, s.now(), req.Retention().Microseconds(), limit, limes.MaxDelegationDepth)
	if err != nil {
		return limes.PurgeResult{}, fmt.Errorf("capability/postgres: purge: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return limes.PurgeResult{}, fmt.Errorf("capability/postgres: purge commit: %w", err)
	}
	return limes.PurgeResult{Purged: tag.RowsAffected()}, nil
}

// ListByPrincipal implements limes.Store. Cursor is the last seen
// id encoded as a hex string; a follow-up page seeks past it. Page size
// is bounded by Limit (default 50, max 500).
func (s *Store) ListByPrincipal(ctx context.Context, args limes.ListByPrincipalRequest) (limes.Page[limes.Capability], error) {
	if err := args.Validate(); err != nil {
		return limes.Page[limes.Capability]{}, err
	}
	if args.Cursor != "" {
		if _, err := uuid.Parse(args.Cursor); err != nil {
			return limes.Page[limes.Capability]{}, fmt.Errorf("%w: capability/postgres: cursor %q", limes.ErrInvalidRequest, args.Cursor)
		}
	}
	limit := limes.PageLimit(args.Limit)

	// Build clauses incrementally so the query plan stays readable.
	whereExtra := ""
	bindArgs := []any{args.TenantID, string(args.PrincipalType), args.Subject}
	if !args.IncludeExpired {
		bindArgs = append(bindArgs, s.now())
		whereExtra += fmt.Sprintf(" AND cr.expires_at > $%d::timestamptz", len(bindArgs))
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
		return limes.Page[limes.Capability]{}, fmt.Errorf("capability/postgres: list: %w", err)
	}
	defer rows.Close()

	out := make([]limes.Capability, 0, limit)
	for rows.Next() {
		c, err := scanRow(rows)
		if err != nil {
			return limes.Page[limes.Capability]{}, err
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return limes.Page[limes.Capability]{}, fmt.Errorf("capability/postgres: list scan: %w", err)
	}

	page := limes.Page[limes.Capability]{Items: out}
	if len(out) > limit {
		page.Items = out[:limit]
		// Seek past the last row RETURNED, not past the overflow row we
		// fetched to detect the next page — the overflow row belongs to
		// the next page and seeking past it drops it entirely.
		page.Next = page.Items[limit-1].ID.String()
	}
	return page, nil
}

// scanRow is the shared row decoder for GetRecord / ListByPrincipal. Accepts
// any pgx Scanner so it works against single-row and multi-row results.
type scanner interface {
	Scan(dest ...any) error
}

func scanRow(r scanner) (*limes.Capability, error) {
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

	principal, err := unmarshalPrincipal(principalRaw)
	if err != nil {
		return nil, err
	}
	// Defensive: principal payload sometimes lacks tenant_id when it was
	// written by a buggy issuer. Fall back to the column we indexed on.
	if principal.TenantID == uuid.Nil {
		principal.TenantID = tenantID
	}
	if principal.Type == "" {
		principal.Type = limes.PrincipalType(kind)
	}
	if principal.Subject == "" {
		principal.Subject = subject
	}

	cav, err := unmarshalCaveats(caveats)
	if err != nil {
		return nil, err
	}

	out := &limes.Capability{
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
var _ limes.Store = (*Store)(nil)
