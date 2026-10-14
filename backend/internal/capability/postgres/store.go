// Package postgres keeps Paladin's capabilities in PostgreSQL. The records,
// their revocations, the meter and the tenant budgets are
// github.com/oleg-tkachuk/limes/pgstore's, in its own schema; this package
// scopes them the way every other Paladin table is scoped, and holds what
// pgstore does not: the DPoP replay cache and the revocation watcher.
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
	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// Option configures the stores New and NewUsageStore build.
type Option func(*options)

type options struct {
	now         func() time.Time
	defaultUnit string
}

// WithClock runs the stores on now instead of the wall clock. For tests that
// move time; the server runs on the wall clock.
func WithClock(now func() time.Time) Option {
	return func(o *options) { o.now = now }
}

// WithDefaultUnit makes unit — the platform's currency, capability.currency —
// what a tenant budget set with no unit is first written in. Without it that
// is limes.DefaultUnitCode, the unit limes reads an unnamed one as.
func WithDefaultUnit(unit string) Option {
	return func(o *options) { o.defaultUnit = unit }
}

// config is the stores' configuration over pool. pgstore refuses a nil pool
// when a store is built.
func config(pool *pgxpool.Pool, opts []Option) (pgstore.Config, options, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return pgstore.Config{
		DB: pool, Now: o.now, Scope: Scope, CheckScope: pgstore.CheckSchemaScope, CheckTenant: checkTenant,
	}, o, nil
}

// New is the store of capability records, their revocations and those of
// their Biscuit copies.
func New(pool *pgxpool.Pool, opts ...Option) (*pgstore.Store, error) {
	cfg, _, err := config(pool, opts)
	if err != nil {
		return nil, err
	}
	return pgstore.NewStore(cfg)
}

// NewLedger is the reader of the charges ledger, for billing and reports,
// scoped as the stores are.
func NewLedger(pool *pgxpool.Pool, opts ...Option) (*pgstore.Ledger, error) {
	cfg, _, err := config(pool, opts)
	if err != nil {
		return nil, err
	}
	return pgstore.NewLedger(cfg)
}

// UsageStore is pgstore's meter, a tenant budget set with no unit written in
// the platform's currency.
type UsageStore struct {
	*pgstore.UsageStore
	defaultUnit string
}

var (
	_ limes.UsageStore[pgx.Tx] = (*UsageStore)(nil)
	_ limes.CopyUsageReader    = (*UsageStore)(nil)
)

// NewUsageStore is the meter: the counters, holds and charges of every
// capability, and the tenant budgets.
func NewUsageStore(pool *pgxpool.Pool, opts ...Option) (*UsageStore, error) {
	cfg, o, err := config(pool, opts)
	if err != nil {
		return nil, err
	}
	u, err := pgstore.NewUsageStore(cfg)
	if err != nil {
		return nil, err
	}
	return &UsageStore{UsageStore: u, defaultUnit: o.defaultUnit}, nil
}

// SetTenantBudget implements limes.UsageStore. A first budget — expected at
// version 0, so none is on record — that names no unit is written in the
// default unit; a later one with no unit keeps the unit it has.
func (s *UsageStore) SetTenantBudget(ctx context.Context, req limes.SetTenantBudgetRequest) (limes.TenantBudget, error) {
	if req.UnitCode == "" && req.ExpectedVersion == 0 {
		req.UnitCode = s.defaultUnit
	}
	return s.UsageStore.SetTenantBudget(ctx, req)
}

// Scope scopes a transaction pgstore opens the way the request pool's
// PrepareConn hook scopes every other connection (store/postgres.EnableRLS),
// from the request's auth context: a platform-wide read (auth.WithCrossTenantRead)
// reads every tenant and writes none, as paladin.cross_tenant widens only
// reads; a request acting on a tenant (auth.EffectiveTenant) is scoped to it.
// A call with neither — housekeeping, which runs on a timer with no principal
// — is scoped as its pgstore context says (pgstore.WithAllTenants), and with
// none sees no row.
//
// It sets the schema's own settings, so the stores check every call whose
// request names a tenant against them (pgstore.CheckSchemaScope): a call
// scoped to another tenant fails as limes.ErrScopeMismatch rather than as
// rows that are not there.
func Scope(ctx context.Context, tx pgx.Tx) error {
	if auth.CrossTenantRead(ctx) {
		return pgstore.ScopeFromContext(pgstore.WithAllTenantsReadOnly(ctx), tx)
	}
	if tenant, err := auth.EffectiveTenant(ctx); err == nil {
		return pgstore.ScopeFromContext(pgstore.WithTenant(ctx, tenant), tx)
	}
	return pgstore.ScopeFromContext(ctx, tx)
}

// ReadAllTenants is ctx for a platform-wide read of the stores — a lookup of
// a capability's tenant, the budget summary: every tenant's rows to read and
// none to write. The stores refuse a call that writes under it with
// limes.ErrScopeMismatch before it begins.
func ReadAllTenants(ctx context.Context) context.Context {
	return pgstore.WithAllTenantsReadOnly(auth.WithCrossTenantRead(ctx))
}

// InScope runs fn in a transaction on pool, scoped by Scope: for a reader of
// the limes schema outside the stores, such as billing.
func InScope(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := Scope(ctx, tx); err != nil {
			return fmt.Errorf("capability/postgres: scope: %w", err)
		}
		return fn(tx)
	})
}

// checkTenant refuses a capability for a tenant Paladin does not hold or holds
// in the trash, and keeps the tenant from being purged until the record is
// written.
func checkTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
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
