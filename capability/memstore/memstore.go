// Package memstore is an in-memory reference implementation of the capability
// module's storage contracts.
//
// It exists for three reasons, in order of importance:
//
//  1. It proves the contracts are satisfiable without a relational database
//     (FR-005) — the module's own suite runs against it with no database, no
//     network and no container (SC-006).
//  2. It is the worked example a third party reads before writing their own
//     store. Everything here is deliberately obvious rather than fast.
//  3. Its Charge carries a staging-commit semantic, which is what makes the
//     atomicity guarantee (FR-011/SC-008) demonstrable at all. Without one,
//     no implementation in the module could show that a failed side effect
//     leaves counters unmutated.
//
// It is NOT a production store: everything is a map behind one mutex, nothing
// survives a restart, and ListByPrincipal ignores paging beyond Limit.
package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
)

// Store implements capability.Store — capability records and revocations.
//
// It is a SEPARATE type from UsageStore because the two contracts both declare
// a method named Get with different signatures, so no single type can satisfy
// both. Paladin's relational implementation splits them for exactly this reason;
// a third-party implementer will hit the same constraint and should expect to
// write two types too.
type Store[TX any] struct {
	mu sync.Mutex

	caps    map[uuid.UUID]capability.Capability
	revoked map[uuid.UUID]bool
	nowFn   func() time.Time
}

// UsageStore implements capability.UsageStore[TX] — request and spend counters.
//
// TX is the consumer's transaction handle. This store has no transactions of
// its own, so it never constructs one unless given a factory; it only threads
// the handle back to onCharged. See contracts/module-api.md §1.2.
type UsageStore[TX any] struct {
	mu sync.Mutex

	usage     map[uuid.UUID]capability.Usage
	budgets   map[uuid.UUID]capability.TenantBudget
	ledger    []LedgerEntry
	caps      map[uuid.UUID]capability.Capability // shared view, for PurgeOrphans
	nowFn     func() time.Time
	txFactory func() TX
}

// LedgerEntry records one committed charge. Exposed so tests (and readers)
// can assert that a rolled-back charge left no trace.
type LedgerEntry struct {
	CapabilityID uuid.UUID
	TenantID     uuid.UUID
	Amount       float64
	UnitCode     string
	Op           string
	Actor        string
	At           time.Time
}

// New returns an empty store. TX is inferred from the call site:
//
//	memstore.New[struct{}]()   // consumer with no transactions
//	memstore.New[pgx.Tx]()     // consumer threading a real handle
func New[TX any]() *Store[TX] {
	return &Store[TX]{
		caps:    map[uuid.UUID]capability.Capability{},
		revoked: map[uuid.UUID]bool{},
		nowFn:   time.Now,
	}
}

// NewUsage returns an empty usage store. Pass the record store when
// PurgeOrphans needs to know which capabilities still exist.
func NewUsage[TX any](records *Store[TX]) *UsageStore[TX] {
	u := &UsageStore[TX]{
		usage:   map[uuid.UUID]capability.Usage{},
		budgets: map[uuid.UUID]capability.TenantBudget{},
		nowFn:   time.Now,
	}
	if records != nil {
		u.caps = records.caps
	}
	return u
}

// WithClock pins the clock on the usage store.
func (s *UsageStore[TX]) WithClock(now func() time.Time) *UsageStore[TX] {
	s.nowFn = now
	return s
}

// WithTxFactory supplies the handle passed to onCharged.
func (s *UsageStore[TX]) WithTxFactory(f func() TX) *UsageStore[TX] {
	s.txFactory = f
	return s
}

// WithClock pins the clock. Tests use it so timestamps are deterministic.
func (s *Store[TX]) WithClock(now func() time.Time) *Store[TX] {
	s.nowFn = now
	return s
}

// Ledger returns a copy of the committed charge ledger.
func (s *UsageStore[TX]) Ledger() []LedgerEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LedgerEntry(nil), s.ledger...)
}

// ─── capability.Store ──────────────────────────────────────────────────────

func (s *Store[TX]) Insert(_ context.Context, c capability.Capability) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.caps[c.ID] = c
	return nil
}

// Get returns capability.ErrNotFound when absent. Callers treat that as
// forgery, not as a missing entity — a syntactically valid token with no
// record was minted by someone else.
func (s *Store[TX]) Get(_ context.Context, id uuid.UUID) (*capability.Capability, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.caps[id]
	if !ok {
		return nil, capability.ErrNotFound
	}
	out := c
	return &out, nil
}

func (s *Store[TX]) IsRevoked(_ context.Context, id uuid.UUID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revoked[id], nil
}

// Revoke is idempotent. CascadeChildren walks the delegation tree by ParentID
// so revoking an orchestrator takes the sub-agents it spawned with it.
func (s *Store[TX]) Revoke(_ context.Context, args capability.RevokeArgs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[args.ID] = true
	if !args.CascadeChildren {
		return nil
	}
	// Repeat to depth: each pass revokes children of anything already revoked,
	// until a pass changes nothing. Obvious beats clever here.
	for {
		changed := false
		for id, c := range s.caps {
			if s.revoked[id] || c.ParentID == uuid.Nil {
				continue
			}
			if s.revoked[c.ParentID] {
				s.revoked[id] = true
				changed = true
			}
		}
		if !changed {
			return nil
		}
	}
}

func (s *Store[TX]) PurgeExpired(_ context.Context, expiredFor time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.nowFn().Add(-expiredFor)
	var n int64
	for id, c := range s.caps {
		if !c.ExpiresAt.IsZero() && c.ExpiresAt.Before(cutoff) {
			delete(s.caps, id)
			delete(s.revoked, id)
			n++
		}
	}
	return n, nil
}

func (s *Store[TX]) ListByPrincipal(_ context.Context, args capability.ListByPrincipalArgs) ([]capability.Capability, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nowFn()
	out := make([]capability.Capability, 0, len(s.caps))
	for id, c := range s.caps {
		if args.TenantID != uuid.Nil && c.Subject.TenantID != args.TenantID {
			continue
		}
		if args.PrincipalT != "" && c.Subject.Type != args.PrincipalT {
			continue
		}
		if args.Subject != "" && c.Subject.Subject != args.Subject {
			continue
		}
		if !args.IncludeExpired && !c.ExpiresAt.IsZero() && c.ExpiresAt.Before(now) {
			continue
		}
		if !args.IncludeRevoked && s.revoked[id] {
			continue
		}
		out = append(out, c)
	}
	// Stable order so tests and readers see the same sequence every run.
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	if args.Limit > 0 && int(args.Limit) < len(out) {
		out = out[:args.Limit]
	}
	return out, "", nil
}

// ─── capability.UsageStore[TX] ─────────────────────────────────────────────

// BumpRequest returns ErrRequestLimitExceeded WITHOUT mutating when the
// increment would cross the ceiling. A rejected call must be safe to retry.
func (s *UsageStore[TX]) BumpRequest(_ context.Context, capID uuid.UUID, maxRequests int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.usage[capID]
	next := u.RequestCount + 1
	if maxRequests > 0 && next > maxRequests {
		return u.RequestCount, capability.ErrRequestLimitExceeded
	}
	u.CapabilityID = capID
	u.RequestCount = next
	s.usage[capID] = u
	return next, nil
}

// Charge applies the two-ceiling rule and commits atomically.
//
// The staging semantic is the point of this method: every mutation is computed
// into locals, onCharged runs, and only if it succeeds are the counters and the
// ledger published. A rejection by either ceiling, or an error from onCharged,
// therefore leaves BOTH counters and the ledger exactly as they were — which
// is the guarantee FR-011/SC-008 require and which nothing in the repository
// tested before this store existed.
func (s *UsageStore[TX]) Charge(
	ctx context.Context,
	capID uuid.UUID,
	amount, maxBudget float64,
	unitCode string,
	tenantID uuid.UUID,
	op string,
	actor string,
	onCharged func(ctx context.Context, tx TX) error,
) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	unit, err := capability.NormaliseUnitCode(unitCode)
	if err != nil {
		return 0, err
	}

	// ── stage: compute, do not publish ──
	u := s.usage[capID]
	stagedSpent := u.SpentAmount + amount

	// 1. capability ceiling
	if maxBudget > 0 && stagedSpent > maxBudget {
		return u.SpentAmount, capability.ErrBudgetExceeded
	}

	// 2. tenant aggregate ceiling
	var stagedBudget capability.TenantBudget
	haveBudget := false
	if tenantID != uuid.Nil {
		if b, ok := s.budgets[tenantID]; ok {
			stagedBudget, haveBudget = b, true
			stagedBudget.SpentAmount = b.SpentAmount + amount
			if b.MaxBudgetAmount > 0 && stagedBudget.SpentAmount > b.MaxBudgetAmount {
				return u.SpentAmount, capability.ErrTenantBudgetExceeded
			}
		}
	}

	// 3. the caller's side effect, on the caller's handle
	if onCharged != nil {
		var tx TX
		if s.txFactory != nil {
			tx = s.txFactory()
		}
		if err := onCharged(ctx, tx); err != nil {
			// Nothing has been published — the counters and ledger are
			// untouched, so there is nothing to compensate.
			return u.SpentAmount, err
		}
	}

	// ── commit: publish every staged mutation together ──
	u.CapabilityID = capID
	u.SpentAmount = stagedSpent
	u.UnitCode = unit
	s.usage[capID] = u
	if haveBudget {
		stagedBudget.UpdatedAt = s.nowFn()
		s.budgets[tenantID] = stagedBudget
	}
	s.ledger = append(s.ledger, LedgerEntry{
		CapabilityID: capID, TenantID: tenantID, Amount: amount,
		UnitCode: unit, Op: op, Actor: actor, At: s.nowFn(),
	})
	return stagedSpent, nil
}

func (s *UsageStore[TX]) RefundCapability(_ context.Context, capID uuid.UUID, amount float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.usage[capID]
	u.CapabilityID = capID
	u.SpentAmount = max0(u.SpentAmount - amount)
	s.usage[capID] = u
	return nil
}

func (s *UsageStore[TX]) RefundTenant(_ context.Context, tenantID uuid.UUID, amount float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.budgets[tenantID]; ok {
		b.SpentAmount = max0(b.SpentAmount - amount)
		b.UpdatedAt = s.nowFn()
		s.budgets[tenantID] = b
	}
	return nil
}

func (s *UsageStore[TX]) Get(_ context.Context, capID uuid.UUID) (capability.Usage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.usage[capID]
	if !ok {
		return capability.Usage{}, capability.ErrUsageNotFound
	}
	return u, nil
}

func (s *UsageStore[TX]) GetTenantBudget(_ context.Context, tenantID uuid.UUID) (capability.TenantBudget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.budgets[tenantID]
	if !ok {
		return capability.TenantBudget{}, capability.ErrTenantBudgetNotFound
	}
	return b, nil
}

func (s *UsageStore[TX]) SetTenantBudget(_ context.Context, args capability.SetTenantBudgetArgs) (capability.TenantBudget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b := s.budgets[args.TenantID]
	unit := args.UnitCode
	if unit == "" {
		unit = b.UnitCode
	}
	normalised, err := capability.NormaliseUnitCode(unit)
	if err != nil {
		return capability.TenantBudget{}, err
	}

	b.TenantID = args.TenantID
	b.MaxBudgetAmount = args.MaxBudgetAmount
	b.UnitCode = normalised
	b.PeriodEnd = args.PeriodEnd
	if b.PeriodStart.IsZero() {
		b.PeriodStart = s.nowFn()
	}
	b.UpdatedAt = s.nowFn()
	s.budgets[args.TenantID] = b
	return b, nil
}

func (s *UsageStore[TX]) ListTenantBudgets(_ context.Context, args capability.ListTenantBudgetsArgs) ([]capability.TenantBudgetSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]capability.TenantBudgetSummary, 0, len(s.budgets))
	for _, b := range s.budgets {
		unlimited := b.MaxBudgetAmount <= 0
		if args.UnlimitedOnly && !unlimited {
			continue
		}
		var pct float64
		if !unlimited {
			pct = b.SpentAmount / b.MaxBudgetAmount * 100
		}
		if args.ThresholdPct > 0 && pct < args.ThresholdPct {
			continue
		}
		out = append(out, capability.TenantBudgetSummary{
			TenantID: b.TenantID, Budget: b, UtilisationPct: pct,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].TenantID.String() < out[j].TenantID.String()
	})
	if args.Limit > 0 && int(args.Limit) < len(out) {
		out = out[:args.Limit]
	}
	return out, nil
}

func (s *UsageStore[TX]) Delete(_ context.Context, capID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.usage, capID)
	return nil
}

func (s *UsageStore[TX]) PurgeOrphans(_ context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for id := range s.usage {
		if _, ok := s.caps[id]; !ok {
			delete(s.usage, id)
			n++
		}
	}
	return n, nil
}

func max0(f float64) float64 {
	if f < 0 {
		return 0
	}
	return f
}

// Compile-time proof that both contracts are satisfied by a consumer holding
// nothing but this module. If either assertion breaks, a third party cannot
// implement the contract either — which is the failure FR-004 guards against.
var (
	_ capability.Store                = (*Store[struct{}])(nil)
	_ capability.UsageStore[struct{}] = (*UsageStore[struct{}])(nil)
)
