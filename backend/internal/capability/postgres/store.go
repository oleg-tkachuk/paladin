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
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
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

// NewCensus is the census of capability records, for the platform's
// reports, scoped as the stores are.
func NewCensus(pool *pgxpool.Pool, opts ...Option) (*pgstore.Census, error) {
	cfg, _, err := config(pool, opts)
	if err != nil {
		return nil, err
	}
	return pgstore.NewCensus(cfg)
}

// UsageStore is pgstore's meter, a tenant budget set with no unit written in
// the platform's currency.
type UsageStore struct {
	*pgstore.UsageStore
	defaultUnit string
}

var (
	_ meter.UsageStore      = (*UsageStore)(nil)
	_ meter.CopyUsageReader = (*UsageStore)(nil)
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

// SetTenantBudget implements meter.UsageStore. A first budget — expected at
// version 0, so none is on record — that names no unit is written in the
// default unit; a later one with no unit keeps the unit it has, which limes
// needs named for any ceiling that lets something be spent.
func (s *UsageStore) SetTenantBudget(ctx context.Context, req meter.SetTenantBudgetRequest) (meter.TenantBudget, error) {
	if req.Ceiling.UnitCode == "" {
		if req.ExpectedVersion == 0 {
			req.Ceiling.UnitCode = s.defaultUnit
		} else {
			current, err := s.GetTenantBudget(ctx, req.TenantID)
			if err != nil {
				return meter.TenantBudget{}, err
			}
			req.Ceiling.UnitCode = current.Ceiling.UnitCode
		}
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
// rows that are not there. pgstore refuses a write under a platform-wide
// read the same way, before it writes.
func Scope(ctx context.Context, tx pgx.Tx) error {
	if auth.CrossTenantRead(ctx) {
		return pgstore.ScopeFromContext(pgstore.WithAllTenantsReadOnly(ctx), tx)
	}
	if tenant, err := auth.EffectiveTenant(ctx); err == nil {
		return pgstore.ScopeFromContext(pgstore.WithTenant(ctx, tenant), tx)
	}
	return pgstore.ScopeFromContext(ctx, tx)
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

// TenantScopedIssuance writes each capability under its own tenant, for an
// issuer that runs where no principal acts on a tenant: the delegation
// issuer at the token endpoint (ADR-0041). Through store alone the row
// would land under no tenant and RLS refuse it. The caller's own check is
// the authorization for the write — the exchange policy, which read the
// tenant from IAM and signs it into the capability — as CapabilityService's
// Cedar gate is for its own issuance.
func TenantScopedIssuance(store limes.IssuanceStore) limes.IssuanceStore {
	return tenantScopedIssuance{store}
}

type tenantScopedIssuance struct{ limes.IssuanceStore }

func (s tenantScopedIssuance) Insert(ctx context.Context, req limes.InsertRequest) error {
	return s.IssuanceStore.Insert(auth.WithActingTenant(ctx, req.Capability.Subject.TenantID), req)
}
