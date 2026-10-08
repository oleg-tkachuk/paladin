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
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
)

// Store implements capability.Store — capability records and revocations.
//
// It is a separate type from UsageStore so that each reads as the contract it
// implements: records and revocations here, counters and the ledger there.
type Store[TX any] struct {
	mu sync.Mutex

	caps     map[uuid.UUID]capability.Capability
	issuedBy map[uuid.UUID]capability.Principal
	revoked  map[uuid.UUID]capability.Revocation
	// revokedCopies maps a revoked Biscuit copy's revocation id to the
	// capability it belongs to.
	revokedCopies map[string]capability.BiscuitRevocation
	nowFn         func() time.Time
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
	holds     map[uuid.UUID]hold
	nowFn     func() time.Time
	txFactory func() TX

	// copies counts each Biscuit copy that set limits of its own, by its
	// block's revocation id.
	copies map[string]copyCounter
}

// copyCounter is one Biscuit copy's own counters.
type copyCounter struct {
	capID    uuid.UUID // the capability the copy belongs to
	requests int64
	spent    float64
	reserved float64
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
	// Copies are the revocation ids of the Biscuit copies the charge also
	// debited, innermost first.
	Copies [][]byte
	// ExternalRef is the charge's ChargeRequest.ExternalRef; empty for none.
	ExternalRef string
	// ReservationID is the reservation the charge settled; uuid.Nil for a
	// charge made directly.
	ReservationID uuid.UUID
	// Overrun reports that the charge crossed a ceiling (OverrunRecord).
	Overrun bool
}

// New returns an empty store. TX is inferred from the call site:
//
//	memstore.New[struct{}]()   // consumer with no transactions
//	memstore.New[pgx.Tx]()     // consumer threading a real handle
func New[TX any]() *Store[TX] {
	return &Store[TX]{
		caps:     map[uuid.UUID]capability.Capability{},
		issuedBy: map[uuid.UUID]capability.Principal{},
		revoked:  map[uuid.UUID]capability.Revocation{},
		nowFn:    time.Now,

		revokedCopies: map[string]capability.BiscuitRevocation{},
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
		holds:   map[uuid.UUID]hold{},
		copies:  map[string]copyCounter{},
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
	if _, ok := s.caps[c.ID]; ok {
		return fmt.Errorf("%w: %s", capability.ErrAlreadyExists, c.ID)
	}
	s.caps[c.ID] = c
	s.issuedBy[c.ID] = issuedBy
	return nil
}

// GetRecord returns the capability, who issued it and its own revocation.
func (s *Store[TX]) GetRecord(_ context.Context, id uuid.UUID) (capability.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.caps[id]
	if !ok {
		return capability.Record{}, capability.ErrNotFound
	}
	rec := capability.Record{Capability: c, IssuedBy: s.issuedBy[id]}
	if r, ok := s.revoked[id]; ok {
		rec.Revocation = &r
	}
	return rec, nil
}

// revokeLocked writes id's revocation entry unless it has one: the first
// entry stands, as a repeated revoke is a no-op. s.mu must be held.
func (s *Store[TX]) revokeLocked(id uuid.UUID, r capability.Revocation) {
	if _, ok := s.revoked[id]; !ok {
		s.revoked[id] = r
	}
}

// isRevokedItselfLocked reports whether id has a revocation entry of its own.
// s.mu must be held.
func (s *Store[TX]) isRevokedItselfLocked(id uuid.UUID) bool {
	_, ok := s.revoked[id]
	return ok
}

// Get returns capability.ErrNotFound when absent. Callers treat that as
// forgery, not as a missing entity — a syntactically valid token with no
// record was minted by someone else.
func (s *Store[TX]) Get(_ context.Context, id uuid.UUID) (capability.Capability, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.caps[id]
	if !ok {
		return capability.Capability{}, capability.ErrNotFound
	}
	return c, nil
}

// IsRevoked answers for the whole delegation chain: a capability is revoked
// when it, or any ancestor still on record, is.
func (s *Store[TX]) IsRevoked(_ context.Context, id uuid.UUID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.revoked[id]; ok {
		return true, nil
	}
	for _, a := range s.ancestorsLocked(id) {
		if _, ok := s.revoked[a.ID]; ok {
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
func (s *Store[TX]) Revoke(_ context.Context, args capability.RevokeRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.caps[args.ID]; !ok {
		return capability.ErrNotFound
	}
	entry := capability.Revocation{
		RevokedAt: s.nowFn(), Reason: args.Reason, Actor: args.Actor, Cascade: args.CascadeChildren,
	}
	s.revokeLocked(args.ID, entry)
	if !args.CascadeChildren {
		return nil
	}
	// Breadth-first down the delegation tree from args.ID alone, to the
	// depth the relational store walks: a capability revoked earlier without
	// cascade elsewhere in the store is not a root of this walk.
	level := []uuid.UUID{args.ID}
	for depth := 0; len(level) > 0 && depth < maxLineageDepth; depth++ {
		var next []uuid.UUID
		for id, c := range s.caps {
			if slices.Contains(level, c.ParentID) {
				s.revokeLocked(id, entry)
				next = append(next, id)
			}
		}
		level = next
	}
	return nil
}

// IsBiscuitRevoked implements capability.BiscuitRevocationLookup.
func (s *Store[TX]) IsBiscuitRevoked(_ context.Context, revocationIDs [][]byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range revocationIDs {
		if _, ok := s.revokedCopies[string(id)]; ok {
			return true, nil
		}
	}
	return false, nil
}

// RevokeBiscuit implements capability.BiscuitRevocationStore.
func (s *Store[TX]) RevokeBiscuit(_ context.Context, args capability.RevokeBiscuitRequest) error {
	if len(args.RevocationID) == 0 {
		return fmt.Errorf("%w: memstore: revocation id required", capability.ErrInvalidRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.caps[args.CapabilityID]; !ok {
		return capability.ErrNotFound
	}
	if _, ok := s.revokedCopies[string(args.RevocationID)]; !ok { // the first entry stands
		s.revokedCopies[string(args.RevocationID)] = capability.BiscuitRevocation{
			CapabilityID: args.CapabilityID, RevocationID: slices.Clone(args.RevocationID),
			RevokedAt: s.nowFn(), Reason: args.Reason, Actor: args.Actor,
		}
	}
	return nil
}

// GetBiscuitRevocation returns what RevokeBiscuit wrote for revocationID.
func (s *Store[TX]) GetBiscuitRevocation(_ context.Context, revocationID []byte) (capability.BiscuitRevocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.revokedCopies[string(revocationID)]
	if !ok {
		return capability.BiscuitRevocation{}, capability.ErrNotFound
	}
	r.RevocationID = slices.Clone(r.RevocationID)
	return r, nil
}

func (s *Store[TX]) PurgeExpired(_ context.Context, expiredFor time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.nowFn().Add(-expiredFor)
	var n int64
	for id, c := range s.caps {
		if c.ExpiresAt.IsZero() || !c.ExpiresAt.Before(cutoff) {
			continue
		}
		if _, ok := s.revoked[id]; ok {
			delete(s.revoked, id)
			n++
		}
		before := len(s.revokedCopies)
		maps.DeleteFunc(s.revokedCopies, func(_ string, r capability.BiscuitRevocation) bool { return r.CapabilityID == id })
		n += int64(before - len(s.revokedCopies))
	}
	return n, nil
}

func (s *Store[TX]) ListByPrincipal(_ context.Context, req capability.ListByPrincipalRequest) ([]capability.Capability, string, error) {
	if err := req.Validate(); err != nil {
		return nil, "", err
	}
	var after uuid.UUID
	if req.Cursor != "" {
		var err error
		if after, err = uuid.Parse(req.Cursor); err != nil {
			return nil, "", fmt.Errorf("%w: memstore: cursor %q", capability.ErrInvalidRequest, req.Cursor)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nowFn()
	out := make([]capability.Capability, 0, len(s.caps))
	for id, c := range s.caps {
		switch {
		case c.Subject.TenantID != req.TenantID, c.Subject.Type != req.PrincipalType, c.Subject.Subject != req.Subject:
			continue
		case !req.IncludeExpired && !c.ExpiresAt.After(now):
			continue
		case !req.IncludeRevoked && s.isRevokedItselfLocked(id):
			continue
		case req.Cursor != "" && !uuidLess(after, id):
			continue
		}
		out = append(out, c)
	}
	// Ascending id, the order the cursor seeks in.
	sort.Slice(out, func(i, j int) bool { return uuidLess(out[i].ID, out[j].ID) })
	limit := int(capability.PageLimit(req.Limit))
	if len(out) <= limit {
		return out, "", nil
	}
	out = out[:limit]
	return out, out[len(out)-1].ID.String(), nil
}

// maxUtilisationPct is where TenantBudgetSummary.UtilisationPct is clamped:
// a tenant past its ceiling is at 100%, not beyond.
const maxUtilisationPct = 100

// percent turns a fraction into a percentage.
const percent = 100

// uuidLess orders ids as Postgres orders the uuid type: bytewise.
func uuidLess(a, b uuid.UUID) bool { return bytes.Compare(a[:], b[:]) < 0 }

// ─── capability.UsageStore[TX] ─────────────────────────────────────────────

// lineage returns the ancestors whose counters move with capID's.
func (s *UsageStore[TX]) lineage(capID uuid.UUID) []capability.Capability {
	if s.records == nil {
		return nil
	}
	return s.records.ancestors(capID)
}

// Bump returns ErrRequestLimitExceeded WITHOUT mutating when the
// increment would cross the capability's ceiling or any ancestor's. A
// rejected call must be safe to retry.
func (s *UsageStore[TX]) Bump(_ context.Context, req capability.BumpRequest) (int64, error) {
	ancestors := s.lineage(req.CapabilityID)

	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.usage[req.CapabilityID]
	next := u.RequestCount + 1
	for _, c := range req.Copies {
		if c.MaxRequests > 0 && s.copies[string(c.RevocationID)].requests+1 > c.MaxRequests {
			return u.RequestCount, fmt.Errorf("%w: copy %x", capability.ErrRequestLimitExceeded, c.RevocationID)
		}
	}
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
	for _, c := range req.Copies {
		cc := s.copies[string(c.RevocationID)]
		cc.capID = req.CapabilityID
		cc.requests++
		s.copies[string(c.RevocationID)] = cc
	}
	u.CapabilityID = req.CapabilityID
	u.RequestCount = next
	s.usage[req.CapabilityID] = u
	return next, nil
}

// hold is one open reservation and every counter it holds against.
type hold struct {
	id        uuid.UUID
	capID     uuid.UUID
	ancestors []uuid.UUID
	tenantID  uuid.UUID
	amount    float64
	unit      string
	op, actor string
	expires   time.Time
	copies    []capability.CopyCeiling
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
	if err := req.Overrun.Validate(); err != nil {
		return capability.ChargeReceipt{}, err
	}
	if err := capability.ValidateExternalRef(req.ExternalRef); err != nil {
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
	if req.ExternalRef != "" {
		if e, ok := s.findLocked(func(e LedgerEntry) bool {
			return e.CapabilityID == req.CapabilityID && e.ExternalRef == req.ExternalRef
		}); ok {
			return s.replayLocked(e), nil
		}
	}
	return s.chargeLocked(ctx, req, unit, ancestorIDs(ancestors), ancestors, nil, onCharged)
}

// ChargeByRef returns the charge a capability took under externalRef.
func (s *UsageStore[TX]) ChargeByRef(_ context.Context, capID uuid.UUID, externalRef string) (capability.ChargeRecord, error) {
	if externalRef == "" {
		return capability.ChargeRecord{}, capability.ErrChargeNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.findLocked(func(e LedgerEntry) bool {
		return e.CapabilityID == capID && e.ExternalRef == externalRef
	})
	if !ok {
		return capability.ChargeRecord{}, capability.ErrChargeNotFound
	}
	return e.record(), nil
}

// GetCharge returns one charge of the ledger by its id.
func (s *UsageStore[TX]) GetCharge(_ context.Context, chargeID uuid.UUID) (capability.ChargeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.findLocked(func(e LedgerEntry) bool { return e.ID == chargeID })
	if !ok {
		return capability.ChargeRecord{}, capability.ErrChargeNotFound
	}
	return e.record(), nil
}

// record is the entry as the Meter contract reads a charge back.
func (e LedgerEntry) record() capability.ChargeRecord {
	return capability.ChargeRecord{
		ChargeID: e.ID, CapabilityID: e.CapabilityID, Amount: e.Amount, Refunded: e.Refunded,
		UnitCode: e.UnitCode, ExternalRef: e.ExternalRef, Overrun: e.Overrun,
	}
}

// findLocked returns the first ledger entry match accepts. s.mu must be held.
func (s *UsageStore[TX]) findLocked(match func(LedgerEntry) bool) (LedgerEntry, bool) {
	i := slices.IndexFunc(s.ledger, match)
	if i < 0 {
		return LedgerEntry{}, false
	}
	return s.ledger[i], true
}

// replayLocked is the receipt for a charge already in the ledger, returned
// when the same charge arrives again. s.mu must be held.
func (s *UsageStore[TX]) replayLocked(e LedgerEntry) capability.ChargeReceipt {
	return capability.ChargeReceipt{
		ChargeID: e.ID, Spent: s.usage[e.CapabilityID].SpentAmount,
		Replayed: true, Overrun: e.Overrun,
	}
}

func ancestorIDs(ancestors []capability.Capability) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(ancestors))
	for _, a := range ancestors {
		ids = append(ids, a.ID)
	}
	return ids
}

// chargeLocked checks every ceiling and, if all admit the charge, publishes
// it together with the release of settling (when non-nil). Held amounts count
// against each ceiling, except the one being settled. s.mu must be held.
func (s *UsageStore[TX]) chargeLocked(
	ctx context.Context,
	req capability.ChargeRequest,
	unit string,
	ids []uuid.UUID,
	ancestors []capability.Capability,
	settling *hold,
	onCharged func(ctx context.Context, tx TX) error,
) (capability.ChargeReceipt, error) {
	released := 0.0
	reservationID := uuid.Nil
	if settling != nil {
		released = settling.amount
		reservationID = settling.id
	}
	// crossed decides a ceiling the charge would cross: under OverrunRecord
	// the charge goes on and the receipt says so; otherwise it is refused
	// with err, and nothing has been published.
	overrun := false
	crossed := func(err error) error {
		if req.Overrun == capability.OverrunRecord {
			overrun = true
			return nil
		}
		return err
	}

	// ── stage: compute, do not publish ──
	u := s.usage[req.CapabilityID]
	stagedSpent := u.SpentAmount + req.Amount

	// 0. the copy's own ceilings, innermost first
	for _, c := range req.Copies {
		cc := s.copies[string(c.RevocationID)]
		if c.MaxBudgetMicros > 0 && cc.spent+cc.reserved-released+req.Amount > c.MaxBudget() {
			if err := crossed(fmt.Errorf("%w: copy %x", capability.ErrBudgetExceeded, c.RevocationID)); err != nil {
				return capability.ChargeReceipt{Spent: u.SpentAmount}, err
			}
		}
	}

	// 1. capability ceiling
	if req.MaxBudget > 0 && stagedSpent+u.ReservedAmount-released > req.MaxBudget {
		if err := crossed(capability.ErrBudgetExceeded); err != nil {
			return capability.ChargeReceipt{Spent: u.SpentAmount}, err
		}
	}

	// 2. ancestor ceilings
	for _, a := range ancestors {
		au := s.usage[a.ID]
		if limit := a.Caveats.MaxBudgetAmount; limit > 0 && au.SpentAmount+au.ReservedAmount-released+req.Amount > limit {
			if err := crossed(fmt.Errorf("%w: ancestor %s", capability.ErrBudgetExceeded, a.ID)); err != nil {
				return capability.ChargeReceipt{Spent: u.SpentAmount}, err
			}
		}
	}

	// 3. tenant aggregate ceiling
	stagedBudget, haveBudget := s.budgets[req.TenantID]
	if haveBudget {
		stagedBudget.SpentAmount += req.Amount
		stagedBudget.ReservedAmount = max0(stagedBudget.ReservedAmount - released)
		if stagedBudget.MaxBudgetAmount > 0 && stagedBudget.SpentAmount+stagedBudget.ReservedAmount > stagedBudget.MaxBudgetAmount {
			if err := crossed(capability.ErrTenantBudgetExceeded); err != nil {
				return capability.ChargeReceipt{Spent: u.SpentAmount}, err
			}
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
	u.ReservedAmount = max0(u.ReservedAmount - released)
	u.UnitCode = unit
	s.usage[req.CapabilityID] = u
	for _, id := range ids {
		au := s.usage[id]
		au.CapabilityID = id
		au.SpentAmount += req.Amount
		au.ReservedAmount = max0(au.ReservedAmount - released)
		if au.UnitCode == "" {
			au.UnitCode = unit
		}
		s.usage[id] = au
	}
	if haveBudget {
		stagedBudget.UpdatedAt = s.nowFn()
		s.budgets[req.TenantID] = stagedBudget
	}
	copyIDs := make([][]byte, 0, len(req.Copies))
	for _, c := range req.Copies {
		cc := s.copies[string(c.RevocationID)]
		cc.capID = req.CapabilityID
		cc.spent += req.Amount
		cc.reserved = max0(cc.reserved - released)
		s.copies[string(c.RevocationID)] = cc
		copyIDs = append(copyIDs, c.RevocationID)
	}
	entry := LedgerEntry{
		ID: uuid.New(), CapabilityID: req.CapabilityID, Ancestors: ids,
		TenantID: req.TenantID, Amount: req.Amount, UnitCode: unit,
		Op: req.Op, Actor: req.Actor, At: s.nowFn(), Copies: copyIDs,
		ExternalRef: req.ExternalRef, ReservationID: reservationID, Overrun: overrun,
	}
	s.ledger = append(s.ledger, entry)
	return capability.ChargeReceipt{ChargeID: entry.ID, Spent: stagedSpent, Overrun: overrun}, nil
}

// Reserve holds an amount against every ceiling Charge checks.
func (s *UsageStore[TX]) Reserve(_ context.Context, req capability.ReserveRequest) (capability.Reservation, error) {
	if err := capability.ValidateAmount(req.Amount); err != nil {
		return capability.Reservation{}, err
	}
	if req.TenantID == uuid.Nil {
		return capability.Reservation{}, errors.New("memstore: reserve requires TenantID")
	}
	unit, err := capability.NormaliseUnitCode(req.UnitCode)
	if err != nil {
		return capability.Reservation{}, err
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = capability.DefaultReservationTTL
	}
	ancestors := s.lineage(req.CapabilityID)

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, c := range req.Copies {
		cc := s.copies[string(c.RevocationID)]
		if c.MaxBudgetMicros > 0 && cc.spent+cc.reserved+req.Amount > c.MaxBudget() {
			return capability.Reservation{}, fmt.Errorf("%w: copy %x", capability.ErrBudgetExceeded, c.RevocationID)
		}
	}
	u := s.usage[req.CapabilityID]
	if req.MaxBudget > 0 && u.SpentAmount+u.ReservedAmount+req.Amount > req.MaxBudget {
		return capability.Reservation{}, capability.ErrBudgetExceeded
	}
	for _, a := range ancestors {
		au := s.usage[a.ID]
		if limit := a.Caveats.MaxBudgetAmount; limit > 0 && au.SpentAmount+au.ReservedAmount+req.Amount > limit {
			return capability.Reservation{}, fmt.Errorf("%w: ancestor %s", capability.ErrBudgetExceeded, a.ID)
		}
	}
	b, haveBudget := s.budgets[req.TenantID]
	if haveBudget && b.MaxBudgetAmount > 0 && b.SpentAmount+b.ReservedAmount+req.Amount > b.MaxBudgetAmount {
		return capability.Reservation{}, capability.ErrTenantBudgetExceeded
	}

	h := hold{
		capID: req.CapabilityID, ancestors: ancestorIDs(ancestors), tenantID: req.TenantID,
		amount: req.Amount, unit: unit, op: req.Op, actor: req.Actor,
		expires: s.nowFn().Add(ttl), copies: slices.Clone(req.Copies),
	}
	h.id = uuid.New()
	s.applyHoldLocked(h, +1)
	s.holds[h.id] = h
	return h.reservation(), nil
}

// applyHoldLocked adds (sign +1) or removes (sign -1) a hold on every counter.
func (s *UsageStore[TX]) applyHoldLocked(h hold, sign float64) {
	for _, id := range append([]uuid.UUID{h.capID}, h.ancestors...) {
		u := s.usage[id]
		u.CapabilityID = id
		u.ReservedAmount = max0(u.ReservedAmount + sign*h.amount)
		if u.UnitCode == "" {
			u.UnitCode = h.unit
		}
		s.usage[id] = u
	}
	if b, ok := s.budgets[h.tenantID]; ok {
		b.ReservedAmount = max0(b.ReservedAmount + sign*h.amount)
		s.budgets[h.tenantID] = b
	}
	for _, c := range h.copies {
		cc := s.copies[string(c.RevocationID)]
		cc.capID = h.capID
		cc.reserved = max0(cc.reserved + sign*h.amount)
		s.copies[string(c.RevocationID)] = cc
	}
}

// Settle charges the actual cost in place of a reservation.
func (s *UsageStore[TX]) Settle(
	ctx context.Context,
	req capability.SettleRequest,
	onCharged func(ctx context.Context, tx TX) error,
) (capability.ChargeReceipt, error) {
	if err := capability.ValidateAmount(req.Amount); err != nil {
		return capability.ChargeReceipt{}, err
	}
	if err := req.Overrun.Validate(); err != nil {
		return capability.ChargeReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.holds[req.ReservationID]
	if !ok {
		if e, settled := s.findLocked(func(e LedgerEntry) bool { return e.ReservationID == req.ReservationID }); settled {
			return s.replayLocked(e), nil
		}
	}
	if !ok || !h.expires.After(s.nowFn()) {
		return capability.ChargeReceipt{}, capability.ErrReservationNotFound
	}
	var ancestors []capability.Capability
	if s.records != nil {
		ancestors = s.records.ancestors(h.capID)
	}
	receipt, err := s.chargeLocked(ctx, capability.ChargeRequest{
		CapabilityID: h.capID, TenantID: h.tenantID, Amount: req.Amount,
		MaxBudget: req.MaxBudget, UnitCode: h.unit, Op: h.op, Actor: h.actor,
		Copies: h.copies, Overrun: req.Overrun,
	}, h.unit, h.ancestors, ancestors, &h, onCharged)
	if err != nil {
		return receipt, err
	}
	delete(s.holds, req.ReservationID)
	return receipt, nil
}

// reservation is the hold as the Meter contract reads it back.
func (h hold) reservation() capability.Reservation {
	return capability.Reservation{
		ID: h.id, CapabilityID: h.capID, TenantID: h.tenantID, Amount: h.amount, UnitCode: h.unit,
		Op: h.op, Actor: h.actor, Copies: budgetsOnly(h.copies), ExpiresAt: h.expires,
	}
}

// budgetsOnly is copies with their budgets alone, as a hold keeps them.
func budgetsOnly(copies []capability.CopyCeiling) []capability.CopyCeiling {
	if len(copies) == 0 {
		return nil
	}
	out := make([]capability.CopyCeiling, len(copies))
	for i, c := range copies {
		out[i] = capability.CopyCeiling{RevocationID: slices.Clone(c.RevocationID), MaxBudgetMicros: c.MaxBudgetMicros}
	}
	return out
}

// GetReservation returns a hold that still counts, expired or not.
func (s *UsageStore[TX]) GetReservation(_ context.Context, reservationID uuid.UUID) (capability.Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.holds[reservationID]
	if !ok {
		return capability.Reservation{}, capability.ErrReservationNotFound
	}
	return h.reservation(), nil
}

// ListReservations returns the capability's own holds, soonest to expire first.
func (s *UsageStore[TX]) ListReservations(_ context.Context, capID uuid.UUID) ([]capability.Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []capability.Reservation{}
	for _, h := range s.holds {
		if h.capID == capID {
			out = append(out, h.reservation())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ExpiresAt.Equal(out[j].ExpiresAt) {
			return out[i].ExpiresAt.Before(out[j].ExpiresAt)
		}
		return uuidLess(out[i].ID, out[j].ID)
	})
	return out, nil
}

// Release ends a reservation without charging. Idempotent.
func (s *UsageStore[TX]) Release(_ context.Context, reservationID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.holds[reservationID]; ok {
		s.applyHoldLocked(h, -1)
		delete(s.holds, reservationID)
	}
	return nil
}

// ReleaseExpired releases every hold past its expiry.
func (s *UsageStore[TX]) ReleaseExpired(_ context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowFn()
	var n int64
	for id, h := range s.holds {
		if !h.expires.After(now) {
			s.applyHoldLocked(h, -1)
			delete(s.holds, id)
			n++
		}
	}
	return n, nil
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
	for _, id := range e.Copies {
		if cc, ok := s.copies[string(id)]; ok {
			cc.spent = max0(cc.spent - amount)
			s.copies[string(id)] = cc
		}
	}
	e.Refunded += amount
	return amount, nil
}

func (s *UsageStore[TX]) GetUsage(_ context.Context, capID uuid.UUID) (capability.Usage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.usage[capID]
	if !ok {
		return capability.Usage{}, capability.ErrUsageNotFound
	}
	return u, nil
}

// CopyUsage implements capability.CopyUsageReader.
func (s *UsageStore[TX]) CopyUsage(_ context.Context, revocationIDs [][]byte) ([]capability.CopyUsage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []capability.CopyUsage
	for _, id := range revocationIDs {
		if c, ok := s.copies[string(id)]; ok {
			out = append(out, capability.CopyUsage{
				RevocationID: slices.Clone(id), CapabilityID: c.capID,
				RequestCount: c.requests, SpentAmount: c.spent, ReservedAmount: c.reserved,
			})
		}
	}
	return out, nil
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

func (s *UsageStore[TX]) SetTenantBudget(_ context.Context, args capability.SetTenantBudgetRequest) (capability.TenantBudget, error) {
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
	if args.ResetSpend {
		b.SpentAmount = 0
		b.PeriodStart = s.nowFn()
	}
	if b.PeriodStart.IsZero() {
		b.PeriodStart = s.nowFn()
	}
	b.UpdatedAt = s.nowFn()
	b.ResourceVersion++
	s.budgets[args.TenantID] = b
	return b, nil
}

func (s *UsageStore[TX]) ListTenantBudgets(_ context.Context, args capability.ListTenantBudgetsRequest) ([]capability.TenantBudgetSummary, string, error) {
	var after *budgetRow
	if args.Cursor != "" {
		c, err := capability.DecodeTenantBudgetCursor(args.Cursor)
		if err != nil {
			return nil, "", err
		}
		pct, _ := strconv.ParseFloat(c.Utilisation, 64) // DecodeTenantBudgetCursor checked it
		after = &budgetRow{pct: pct, tenant: c.TenantID}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rows := make([]budgetRow, 0, len(s.budgets))
	for _, b := range s.budgets {
		unlimited := b.MaxBudgetAmount <= 0
		if args.UnlimitedOnly && !unlimited {
			continue
		}
		var pct float64
		if !unlimited {
			pct = b.SpentAmount / b.MaxBudgetAmount * percent
		}
		if args.ThresholdPct > 0 && pct < args.ThresholdPct {
			continue
		}
		row := budgetRow{pct: pct, tenant: b.TenantID, budget: b}
		if after != nil && !after.before(row) {
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].before(rows[j]) })

	limit := int(capability.PageLimit(args.Limit))
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = capability.TenantBudgetCursor{
			Utilisation: strconv.FormatFloat(last.pct, 'g', -1, 64), TenantID: last.tenant,
		}.Encode()
	}
	out := make([]capability.TenantBudgetSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, capability.TenantBudgetSummary{
			TenantID: r.tenant, Budget: r.budget, UtilisationPct: min(r.pct, maxUtilisationPct),
		})
	}
	return out, next, nil
}

// budgetRow is a tenant budget in ListTenantBudgets' order.
type budgetRow struct {
	pct    float64 // unclamped utilisation
	tenant uuid.UUID
	budget capability.TenantBudget
}

// before reports whether r comes before o: higher utilisation first, then the
// lower tenant id.
func (r budgetRow) before(o budgetRow) bool {
	if r.pct != o.pct {
		return r.pct > o.pct
	}
	return uuidLess(r.tenant, o.tenant)
}

func (s *UsageStore[TX]) Delete(_ context.Context, capID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.usage, capID)
	maps.DeleteFunc(s.copies, func(_ string, c copyCounter) bool { return c.capID == capID })
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
	maps.DeleteFunc(s.copies, func(_ string, c copyCounter) bool {
		return s.records == nil || !s.records.exists(c.capID)
	})
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

var _ capability.CopyUsageReader = (*UsageStore[struct{}])(nil)
