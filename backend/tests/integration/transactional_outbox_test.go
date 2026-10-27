//go:build integration

// Transactional-outbox atomicity against a real Postgres (ADR-0003).
//
// Both the charge path (capability/postgres.UsageStore.Charge) and the
// audit path (adapters.AuditRepoV2.InsertWithOutbox) enqueue their
// fan-out outbox rows on the SAME transaction as the source write, so
// there is no dual-write window: the source row and the event either
// both commit or both roll back. These tests pin that invariant by
// driving the on-commit hook to success (both land) and to failure
// (neither lands).
package integration

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	capabilitypg "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// insertOutboxOnTx is a stand-in fan-out hook: it writes one
// event_deliveries row on the caller's tx, exactly as
// dispatcher.DispatchTx would. Runs on the BYPASSRLS migrate pool's tx
// so the event_deliveries RLS policy is out of the picture.
// subID is a parameter rather than a fresh uuid because
// event_deliveries.subscription_id is a real FK — the row it points at has to
// have been seeded by the caller, on a pool the transaction can see.
// onTx is fn as the usage store's onCharged hook, which runs it on the
// charge's transaction.
func onTx(fn func(context.Context, pgx.Tx) error) func(context.Context, meter.Charged) error {
	return func(ctx context.Context, _ meter.Charged) error {
		tx, ok := pgstore.TxFrom(ctx)
		if !ok {
			return errors.New("onCharged ran with no transaction in its context")
		}
		return fn(ctx, tx)
	}
}

func insertOutboxOnTx(tenantID, subID uuid.UUID) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO event_deliveries
			   (id, tenant_id, subscription_id, event_type, event_at, event_payload)
			 VALUES ($1, $2, $3, 'paladin.test.event', now(), '{}'::jsonb)`,
			uuid.New(), tenantID, subID,
		)
		return err
	}
}

func countOutbox(t *testing.T, h *pgharness.Harness, tenantID uuid.UUID) int64 {
	t.Helper()
	var n int64
	if err := h.PoolMigrate.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM event_deliveries WHERE tenant_id = $1`, tenantID,
	).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

// newUsageStore is the meter over pool, as the server builds it.
func newUsageStore(t *testing.T, pool *pgxpool.Pool, opts ...capabilitypg.Option) *capabilitypg.UsageStore {
	t.Helper()
	store, err := capabilitypg.NewUsageStore(pool, opts...)
	if err != nil {
		t.Fatalf("usage store: %v", err)
	}
	return store
}

// newLedger is the server's reader of the charges ledger over pool.
func newLedger(t *testing.T, pool *pgxpool.Pool) meter.LedgerReader {
	t.Helper()
	l, err := capabilitypg.NewLedger(pool)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	return l
}

// ledgerSums is the tenant's charges as pgstore's Ledger sums them, by
// operation and actor, over every time a test charges at.
func ledgerSums(t *testing.T, h *pgharness.Harness, tenantID uuid.UUID) []meter.ChargeSum {
	t.Helper()
	page, err := newLedger(t, h.PoolMigrate).SumCharges(context.Background(), meter.SumChargesRequest{
		TenantID: tenantID, From: time.Unix(0, 0), To: time.Now().Add(time.Hour),
		GroupBy: []meter.ChargeDimension{meter.ChargeDimensionOp, meter.ChargeDimensionActor}, Limit: limes.MaxListLimit,
	})
	if err != nil {
		t.Fatalf("sum charges: %v", err)
	}
	return page.Items
}

// countCharges is how many charges the tenant's ledger holds.
func countCharges(t *testing.T, h *pgharness.Harness, tenantID uuid.UUID) int64 {
	t.Helper()
	var n int64
	for _, s := range ledgerSums(t, h, tenantID) {
		n += s.Charges
	}
	return n
}

// seedUnitCode is the unit every seeded capability, charge and tenant budget
// in this package counts in.
const seedUnitCode = "USD"

// seedCapLifetime is how long a seeded capability stays live: past any test.
const seedCapLifetime = time.Hour

// seedTestCapability puts capID on record for tenantID through the capability
// store, so its ceilings land in the columns the meter reads, and returns the
// subject the meter counts it as. The meter refuses a capability not on
// record, so every charge needs one.
func seedTestCapability(t *testing.T, h *pgharness.Harness, capID, tenantID uuid.UUID, subject string, budget limes.BudgetCeiling) limes.MeterSubject {
	t.Helper()
	return seedCapability(t, h, capID, tenantID, subject, budget, seedUnitCode)
}

func seedCapability(t *testing.T, h *pgharness.Harness, capID, tenantID uuid.UUID, subject string, budget limes.BudgetCeiling, unit string) limes.MeterSubject {
	t.Helper()
	ctx := context.Background()
	records, err := capabilitypg.New(h.PoolMigrate)
	if err != nil {
		t.Fatalf("capability store: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	c := limes.Capability{
		ID:       capID,
		Issuer:   "test-issuer",
		Subject:  limes.Principal{Type: limes.PrincipalTypeAgent, TenantID: tenantID, Subject: subject},
		Audience: []string{"admin"},
		Caveats: limes.Caveats{
			Ops:      []limes.Op{limes.OpGet},
			Requests: limes.RequestCeiling{Unlimited: true},
			Budget:   limes.BudgetCeiling{Max: budget.Max, Unlimited: budget.Unlimited, UnitCode: unit},
		},
		IssuedAt:  now,
		ExpiresAt: now.Add(seedCapLifetime),
	}
	issuer := limes.Principal{Type: limes.PrincipalTypeService, Subject: "test-issuer"}
	if err := records.Insert(ctx, limes.InsertRequest{Capability: c, IssuedBy: issuer}); err != nil {
		t.Fatalf("seed capability record: %v", err)
	}
	s, err := limes.StoredMeterSubject(ctx, records, capID)
	if err != nil {
		t.Fatalf("meter subject: %v", err)
	}
	return s
}

// seedCapRecord puts capID on record for tenantID with the given budget
// ceiling and returns its meter subject.
func seedCapRecord(t *testing.T, h *pgharness.Harness, capID, tenantID uuid.UUID, budget limes.BudgetCeiling) limes.MeterSubject {
	t.Helper()
	return seedTestCapability(t, h, capID, tenantID, "agent-1", budget)
}

// outboxCapBudget bounds the capability the outbox tests charge; the charge
// fits under it.
var outboxCapBudget = limes.BudgetCeiling{Max: limes.MustParseAmount("10.0"), UnitCode: seedUnitCode}

// TestCharge_FanoutCommitsOnSameTx: a successful Charge with a fan-out
// hook lands ALL of it atomically — the running-total counters, the
// charges-ledger row, and the outbox row the hook wrote.
func TestCharge_FanoutCommitsOnSameTx(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	store := newUsageStore(t, h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-outbox-ok")
	capID := uuid.New()
	subject := seedCapRecord(t, h, capID, tenantID, outboxCapBudget)

	spent, err := store.Charge(ctx, meter.ChargeRequest{Subject: subject, Amount: limes.MustParseAmount("2.5"), Op: "presign.put", Actor: "agent-1"}, onTx(insertOutboxOnTx(tenantID, seedSubscriptionRow(t, h.PoolMigrate, tenantID))))
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if spent.SpentAmount != limes.MustParseAmount("2.5") {
		t.Errorf("spent = %v, want 2.50", spent.SpentAmount)
	}

	// Counter committed.
	usage, err := store.GetUsage(ctx, capID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.SpentAmount != limes.MustParseAmount("2.5") {
		t.Errorf("cap spent = %v, want 2.50", usage.SpentAmount)
	}
	// Ledger row committed.
	if got := countCharges(t, h, tenantID); got != 1 {
		t.Errorf("charges rows = %d, want 1", got)
	}
	// Outbox row committed on the SAME tx.
	if got := countOutbox(t, h, tenantID); got != 1 {
		t.Errorf("event_deliveries rows = %d, want 1 (fan-out must share the charge tx)", got)
	}
}

// TestCharge_FanoutErrorRollsBackEverything: a fan-out hook that fails
// rolls the WHOLE charge back — no counter bump, no ledger row, no
// outbox row. Proves the outbox write is atomic with the charge (the
// core no-dual-write-window guarantee).
func TestCharge_FanoutErrorRollsBackEverything(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	store := newUsageStore(t, h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-outbox-fail")
	capID := uuid.New()
	subject := seedCapRecord(t, h, capID, tenantID, outboxCapBudget)

	boom := errors.New("fan-out down")
	_, err := store.Charge(ctx, meter.ChargeRequest{Subject: subject, Amount: limes.MustParseAmount("2.5"), Op: "presign.put", Actor: "agent-1"}, func(context.Context, meter.Charged) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("charge err = %v, want it to wrap the fan-out error", err)
	}

	// Counter never committed → no usage row at all.
	if _, err := store.GetUsage(ctx, capID); !errors.Is(err, meter.ErrUsageNotFound) {
		t.Errorf("usage after rollback: err = %v, want ErrUsageNotFound (counter must not have committed)", err)
	}
	// Ledger row rolled back.
	if got := countCharges(t, h, tenantID); got != 0 {
		t.Errorf("charges rows = %d, want 0 (ledger must roll back with the fan-out)", got)
	}
	// Outbox row rolled back.
	if got := countOutbox(t, h, tenantID); got != 0 {
		t.Errorf("event_deliveries rows = %d, want 0", got)
	}
}

// TestAudit_InsertWithOutbox_AtomicCommit: the audit row and the mirror
// outbox row the hook enqueues both commit together.
func TestAudit_InsertWithOutbox_AtomicCommit(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	repo := adapters.NewAuditRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-audit-ok")
	entryID := uuid.NewV7()
	entry := admindomain.AuditEntry{
		EntryID:       entryID,
		ActorSubject:  "admin@local",
		ActorTenantID: tenantID,
		ActorAudience: "paladin-admin",
		Action:        "/paladin.admin.v1.TenantService/CreateTenant",
		ResourceName:  "tenants/" + tenantID.String(),
	}

	if err := repo.InsertWithOutbox(ctx, entry, insertOutboxOnTx(tenantID, seedSubscriptionRow(t, h.PoolMigrate, tenantID))); err != nil {
		t.Fatalf("InsertWithOutbox: %v", err)
	}

	// Audit row committed.
	if _, err := repo.Get(ctx, entryID); err != nil {
		t.Errorf("audit Get after commit: %v, want the row present", err)
	}
	// Mirror outbox row committed on the SAME tx.
	if got := countOutbox(t, h, tenantID); got != 1 {
		t.Errorf("event_deliveries rows = %d, want 1 (mirror must share the audit tx)", got)
	}
}

// TestAudit_InsertWithOutbox_HookErrorRollsBackAudit: a mirror hook that
// fails rolls the audit row back with it — no audit-row-without-event.
func TestAudit_InsertWithOutbox_HookErrorRollsBackAudit(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	repo := adapters.NewAuditRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-audit-fail")
	entryID := uuid.NewV7()
	entry := admindomain.AuditEntry{
		EntryID:       entryID,
		ActorSubject:  "admin@local",
		ActorTenantID: tenantID,
		ActorAudience: "paladin-admin",
		Action:        "/paladin.admin.v1.TenantService/DeleteTenant",
		ResourceName:  "tenants/" + tenantID.String(),
	}

	boom := errors.New("mirror down")
	err := repo.InsertWithOutbox(ctx, entry, func(context.Context, pgx.Tx) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("InsertWithOutbox err = %v, want the mirror error", err)
	}

	// Audit row rolled back.
	if _, err := repo.Get(ctx, entryID); !errors.Is(err, admindomain.ErrNotFound) {
		t.Errorf("audit Get after rollback: err = %v, want ErrNotFound (row must not have committed)", err)
	}
	if got := countOutbox(t, h, tenantID); got != 0 {
		t.Errorf("event_deliveries rows = %d, want 0", got)
	}
}
