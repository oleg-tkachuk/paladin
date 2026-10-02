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
	"errors"
	"fmt"
	"slices"
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

	caps     map[uuid.UUID]capability.Capability
	issuedBy map[uuid.UUID]capability.Principal
	revoked  map[uuid.UUID]bool
	nowFn    func() time.Time
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
	records   *Store[TX] // lineage and ceilings of ancestors; nil = none known
	nowFn     func() time.Time
	txFactory func() TX
}

// LedgerEntry records one committed charge. Exposed so tests (and readers)
// can assert that a rolled-back charge left no trace.
type LedgerEntry struct {
	ID           uuid.UUID
	CapabilityID uuid.UUID
	// Ancestors are the capabilities the charge also debited, nearest
	// first, so a refund returns spend to exactly the same counters.
	Ancestors []uuid.UUID
	TenantID  uuid.UUID
	Amount    float64
	UnitCode  string
	Op        string
	Actor     string
	At        time.Time
	// Refunded is the total refunded from this charge so far.
	Refunded float64
}

// New returns an empty store. TX is inferred from the call site:
//
//	memstore.New[struct{}]()   // consumer with no transactions
//	memstore.New[pgx.Tx]()     // consumer threading a real handle
func New[TX any]() *Store[TX] {
	return &Store[TX]{
		caps:     map[uuid.UUID]capability.Capability{},
		issuedBy: map[uuid.UUID]capability.Principal{},
		revoked:  map[uuid.UUID]bool{},
		nowFn:    time.Now,
	}
}

// NewUsage returns an empty usage store over a record store. The records are
// how it finds each capability's ancestors and their ceilings, and which
// capabilities still exist for PurgeOrphans. nil records means no lineage is
// known: every capability is metered on its own.
func NewUsage[TX any](records *Store[TX]) *UsageStore[TX] {
	return &UsageStore[TX]{
		usage:   map[uuid.UUID]capability.Usage{},
		budgets: map[uuid.UUID]capability.TenantBudget{},
		records: records,
		nowFn:   time.Now,
	}
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

// issuedBy is recorded alongside the capability so the in-memory store answers
// the same attribution questions the relational one does — a test double that
// silently drops the initiator would let an unattributed issuance pass.
func (s *Store[TX]) Insert(_ context.Context, c capability.Capability, issuedBy capability.Principal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.caps[c.ID] = c
	s.issuedBy[c.ID] = issuedBy
	return nil
}

// IssuedBy returns the principal that requested the capability, if known.
func (s *Store[TX]) IssuedBy(id uuid.UUID) (capability.Principal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.issuedBy[id]
	return p, ok
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

// IsRevoked answers for the whole delegation chain: a capability is revoked
// when it, or any ancestor still on record, is.
func (s *Store[TX]) IsRevoked(_ context.Context, id uuid.UUID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked[id] {
		return true, nil
	}
	for _, a := range s.ancestorsLocked(id) {
		if s.revoked[a.ID] {
			return true, nil
		}
	}
	return false, nil
}

// ancestors returns the records of id's ancestors, nearest first, stopping
// at the first one no longer on record. Bounded so a corrupt cycle cannot
// spin forever.
func (s *Store[TX]) ancestors(id uuid.UUID) []capability.Capability {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ancestorsLocked(id)
}

func (s *Store[TX]) ancestorsLocked(id uuid.UUID) []capability.Capability {
	var out []capability.Capability
	c, ok := s.caps[id]
	for depth := 0; ok && c.ParentID != uuid.Nil && depth < maxLineageDepth; depth++ {
		c, ok = s.caps[c.ParentID]
		if ok {
			out = append(out, c)
		}
	}
	return out
}

func (s *Store[TX]) exists(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.caps[id]
	return ok
}

// maxLineageDepth matches the depth guard of the relational store.
const maxLineageDepth = 64

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

// lineage returns the ancestors whose counters move with capID's.
func (s *UsageStore[TX]) lineage(capID uuid.UUID) []capability.Capability {
	if s.records == nil {
		return nil
	}
	return s.records.ancestors(capID)
}

// BumpRequest returns ErrRequestLimitExceeded WITHOUT mutating when the
// increment would cross the capability's ceiling or any ancestor's. A
// rejected call must be safe to retry.
func (s *UsageStore[TX]) BumpRequest(_ context.Context, req capability.RequestBump) (int64, error) {
	ancestors := s.lineage(req.CapabilityID)

	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.usage[req.CapabilityID]
	next := u.RequestCount + 1
	if req.MaxRequests > 0 && next > req.MaxRequests {
		return u.RequestCount, capability.ErrRequestLimitExceeded
	}
	for _, a := range ancestors {
		limit := int64(a.Caveats.MaxRequests)
		if limit > 0 && s.usage[a.ID].RequestCount+1 > limit {
			return u.RequestCount, fmt.Errorf("%w: ancestor %s", capability.ErrRequestLimitExceeded, a.ID)
		}
	}
	for _, a := range ancestors {
		au := s.usage[a.ID]
		au.CapabilityID = a.ID
		au.RequestCount++
		s.usage[a.ID] = au
	}
	u.CapabilityID = req.CapabilityID
	u.RequestCount = next
	s.usage[req.CapabilityID] = u
	return next, nil
}

// Charge applies the ceiling chain and commits atomically.
//
// The staging semantic is the point of this method: every mutation is computed
// into locals, onCharged runs, and only if it succeeds are the counters and the
// ledger published. A rejection by any ceiling, or an error from onCharged,
// therefore leaves EVERY counter and the ledger exactly as they were — which
// is the guarantee FR-011/SC-008 require.
func (s *UsageStore[TX]) Charge(
	ctx context.Context,
	req capability.ChargeRequest,
	onCharged func(ctx context.Context, tx TX) error,
) (capability.ChargeReceipt, error) {
	if err := capability.ValidateAmount(req.Amount); err != nil {
		return capability.ChargeReceipt{}, err
	}
	if req.TenantID == uuid.Nil {
		return capability.ChargeReceipt{}, errors.New("memstore: charge requires TenantID")
	}
	unit, err := capability.NormaliseUnitCode(req.UnitCode)
	if err != nil {
		return capability.ChargeReceipt{}, err
	}
	ancestors := s.lineage(req.CapabilityID)

	s.mu.Lock()
	defer s.mu.Unlock()

	// ── stage: compute, do not publish ──
	u := s.usage[req.CapabilityID]
	stagedSpent := u.SpentAmount + req.Amount

	// 1. capability ceiling
	if req.MaxBudget > 0 && stagedSpent > req.MaxBudget {
		return capability.ChargeReceipt{Spent: u.SpentAmount}, capability.ErrBudgetExceeded
	}

	// 2. ancestor ceilings
	ancestorIDs := make([]uuid.UUID, 0, len(ancestors))
	for _, a := range ancestors {
		if limit := a.Caveats.MaxBudgetAmount; limit > 0 && s.usage[a.ID].SpentAmount+req.Amount > limit {
			return capability.ChargeReceipt{Spent: u.SpentAmount},
				fmt.Errorf("%w: ancestor %s", capability.ErrBudgetExceeded, a.ID)
		}
		ancestorIDs = append(ancestorIDs, a.ID)
	}

	// 3. tenant aggregate ceiling
	stagedBudget, haveBudget := s.budgets[req.TenantID]
	if haveBudget {
		stagedBudget.SpentAmount += req.Amount
		if stagedBudget.MaxBudgetAmount > 0 && stagedBudget.SpentAmount > stagedBudget.MaxBudgetAmount {
			return capability.ChargeReceipt{Spent: u.SpentAmount}, capability.ErrTenantBudgetExceeded
		}
	}

	// 4. the caller's side effect, on the caller's handle
	if onCharged != nil {
		var tx TX
		if s.txFactory != nil {
			tx = s.txFactory()
		}
		if err := onCharged(ctx, tx); err != nil {
			// Nothing has been published — nothing to compensate.
			return capability.ChargeReceipt{Spent: u.SpentAmount}, err
		}
	}

	// ── commit: publish every staged mutation together ──
	u.CapabilityID = req.CapabilityID
	u.SpentAmount = stagedSpent
	u.UnitCode = unit
	s.usage[req.CapabilityID] = u
	for _, id := range ancestorIDs {
		au := s.usage[id]
		au.CapabilityID = id
		au.SpentAmount += req.Amount
		if au.UnitCode == "" {
			au.UnitCode = unit
		}
		s.usage[id] = au
	}
	if haveBudget {
		stagedBudget.UpdatedAt = s.nowFn()
		s.budgets[req.TenantID] = stagedBudget
	}
	entry := LedgerEntry{
		ID: uuid.New(), CapabilityID: req.CapabilityID, Ancestors: ancestorIDs,
		TenantID: req.TenantID, Amount: req.Amount, UnitCode: unit,
		Op: req.Op, Actor: req.Actor, At: s.nowFn(),
	}
	s.ledger = append(s.ledger, entry)
	return capability.ChargeReceipt{ChargeID: entry.ID, Spent: stagedSpent}, nil
}

// Refund returns spend from one ledger entry to the counters it debited.
func (s *UsageStore[TX]) Refund(_ context.Context, req capability.RefundRequest) (float64, error) {
	if err := capability.ValidateAmount(req.Amount); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	i := slices.IndexFunc(s.ledger, func(e LedgerEntry) bool { return e.ID == req.ChargeID })
	if i < 0 {
		return 0, capability.ErrChargeNotFound
	}
	e := &s.ledger[i]
	remaining := e.Amount - e.Refunded
	amount := req.Amount
	if amount == 0 {
		amount = remaining
	}
	if amount > remaining {
		return 0, fmt.Errorf("%w: %v requested, %v of %v left",
			capability.ErrRefundExceedsCharge, amount, remaining, e.Amount)
	}
	if amount == 0 {
		return 0, nil
	}
	for _, id := range append([]uuid.UUID{e.CapabilityID}, e.Ancestors...) {
		if u, ok := s.usage[id]; ok {
			u.SpentAmount = max0(u.SpentAmount - amount)
			s.usage[id] = u
		}
	}
	if b, ok := s.budgets[e.TenantID]; ok {
		b.SpentAmount = max0(b.SpentAmount - amount)
		b.UpdatedAt = s.nowFn()
		s.budgets[e.TenantID] = b
	}
	e.Refunded += amount
	return amount, nil
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

	b, exists := s.budgets[args.TenantID]

	// Same OCC contract as the Postgres store, or tests pass against a
	// memstore that permits writes production refuses. A missing row expects
	// version 0; an existing one expects its current version.
	want := int64(0)
	if exists {
		want = b.ResourceVersion
	}
	if args.ExpectedVersion != want {
		return capability.TenantBudget{}, capability.ErrTenantBudgetVersionMismatch
	}

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
	b.ResourceVersion++
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
		if s.records == nil || !s.records.exists(id) {
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
