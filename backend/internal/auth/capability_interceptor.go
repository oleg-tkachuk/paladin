package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
	"github.com/oleg-tkachuk/paladin/capability"
)

// Header names for capability tokens. Two are accepted:
//
//   - "Authorization: Capability <token>" — normalised RFC 7235-style
//     scheme; tools that already speak Authorization for JWT can switch
//     by changing the scheme keyword.
//
//   - "X-Paladin-Capability: <token>" — convenience for clients that already
//     use Authorization for an OIDC bearer and need a separate slot.
//
// Either is accepted; if both are present, X-Paladin-Capability wins because
// the explicit per-product header is the unambiguous signal.
const (
	HeaderCapability       = paladin.HeaderCapability
	AuthorizationCapScheme = "capability"
)

// capabilityKey is the context value the interceptor stashes the
// verified *capability.Capability under. Read via CapabilityFromContext.
type capabilityKey struct{}

// CapabilityFromContext returns the verified capability for the request,
// or nil when none was supplied / verified. Handlers gate on caveats
// (op set, resource prefix, budget) by calling this and switching on
// the result.
func CapabilityFromContext(ctx context.Context) (*capability.Capability, bool) {
	c, ok := ctx.Value(capabilityKey{}).(*capability.Capability)
	return c, ok && c != nil
}

// WithCapability stamps a verified capability onto a context. Public so
// tests can prepare contexts without going through the interceptor.
func WithCapability(ctx context.Context, c *capability.Capability) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, capabilityKey{}, c)
}

// CapabilityInterceptor is an *additive* Connect interceptor — it
// looks for a capability token on the inbound request and, when present
// and valid, stamps the verified *capability.Capability onto the
// request context.
//
// Crucially: a missing or invalid token is a NO-OP. The interceptor
// does not reject; downstream JWT verification still runs and the
// existing auth path is unaffected. Handlers that *require* a
// capability check the context value and return CodePermissionDenied
// when it's absent.
//
// Audience is the plane label this interceptor is mounted on
// ("data" / "admin" / "iam") — the verifier rejects tokens whose
// `aud` claim doesn't include the plane.
//
// When verifier is nil (capability subsystem disabled) the returned
// interceptor is a pass-through that never touches the context.
//
// usage is the runtime-counter store. When non-nil and the capability
// carries Caveats.MaxRequests > 0, every request bumps the counter
// and rejects when over. When nil, MaxRequests is silently un-enforced
// — the operator opted out by not wiring the store.
//
// SourceIPCIDR is checked against the client address the listener resolved
// from its trusted proxies (clientip.Middleware), never against a forwarding
// header directly: the leftmost entry of one is whatever the caller wrote.
//
// chargePerRequestAmount + chargePerRequestUnit form the auto-charge
// stamped onto the context. The amount is denominated in the unit;
// at handler time, ChargeRequest passes both into UsageStore.Charge.
// Falling back to the capability's own UnitCode happens inside
// ChargeCapability when the stamp's unit is empty.
func CapabilityInterceptor(
	verifier *capability.StandardVerifier,
	audience string,
	usage capability.UsageStore[pgx.Tx],
	chargePerRequestAmount float64,
	chargePerRequestUnit string,
) connect.Interceptor {
	return CapabilityInterceptorWithEvents(verifier, audience, usage,
		chargePerRequestAmount, chargePerRequestUnit, nil)
}

// CapabilityInterceptorWithEvents is the events-aware variant. The
// emitter is stamped on every authenticated request's context so
// ChargeCapability can fan out an `paladin.capability.charged` event AFTER
// the running totals commit. nil emitter = no events (the default —
// gated on cfg.Dispatcher.ChargeEventsEnabled at the wiring layer).
func CapabilityInterceptorWithEvents(
	verifier *capability.StandardVerifier,
	audience string,
	usage capability.UsageStore[pgx.Tx],
	chargePerRequestAmount float64,
	chargePerRequestUnit string,
	emitter ChargeEventEmitter,
) connect.Interceptor {
	if verifier == nil {
		return passthroughInterceptor{}
	}
	return &capabilityInterceptor{
		verifier:               verifier,
		audience:               audience,
		usage:                  usage,
		chargePerRequestAmount: chargePerRequestAmount,
		chargePerRequestUnit:   chargePerRequestUnit,
		emitter:                emitter,
	}
}

// CapabilityEstablishingInterceptor is the data-plane variant, in which a
// verified capability may BE the credential: when the request carries no other
// identity, the capability's tenant becomes the caller's tenant.
//
// It is a separate constructor rather than a flag on the others because the
// choice is security-relevant and belongs at the wiring site, where a reader
// can see which plane grants it. The admin and IAM planes must keep the
// additive interceptor: a capability names a tenant, not a role, so letting it
// establish identity there would produce a caller with no roles for RPCs whose
// policies are role-gated — refused, but for a confusing reason.
func CapabilityEstablishingInterceptor(
	verifier *capability.StandardVerifier,
	audience string,
	usage capability.UsageStore[pgx.Tx],
	chargePerRequestAmount float64,
	chargePerRequestUnit string,
	emitter ChargeEventEmitter,
) connect.Interceptor {
	i := CapabilityInterceptorWithEvents(verifier, audience, usage,
		chargePerRequestAmount, chargePerRequestUnit, emitter)
	if ci, ok := i.(*capabilityInterceptor); ok {
		ci.establishPrincipal = true
	}

	return i
}

type capabilityInterceptor struct {
	verifier *capability.StandardVerifier
	audience string
	// establishPrincipal makes a verified capability an IDENTITY rather than
	// only an extra restriction. On the data plane a capability is the whole
	// credential a caller may present: it names its tenant, the verifier has
	// checked the signature, expiry and revocation, and enforceCaveats has
	// already narrowed what it may do. Without this the request reached the
	// tenant gate with no principal and was refused as unauthenticated, so a
	// capability could only ever narrow a caller who was already
	// authenticated some other way — which made it useless as the mechanism
	// for reaching a tenant whose long-lived credential we deliberately do
	// not hold.
	//
	// An existing principal always wins: a capability presented alongside a
	// JWT or API token stays additive, exactly as before.
	establishPrincipal     bool
	usage                  capability.UsageStore[pgx.Tx]
	chargePerRequestAmount float64
	chargePerRequestUnit   string
	emitter                ChargeEventEmitter
}

func (i *capabilityInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		token := extractCapabilityToken(req.Header().Get(HeaderCapability), req.Header().Get("Authorization"))
		if token == "" {
			return next(ctx, req)
		}
		cap, err := i.verifier.Verify(ctx, token, i.audience)
		if err != nil {
			// Token was supplied AND failed verification. We surface
			// this as PermissionDenied — the caller chose to present
			// a capability and it didn't pass; falling through to JWT
			// silently would mask the misconfiguration.
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		}
		if err := i.enforceCaveats(ctx, cap); err != nil {
			return nil, err
		}
		ctx = WithCapability(ctx, cap)
		ctx, err = i.withCapabilityPrincipal(ctx, cap)
		if err != nil {
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		}
		ctx = WithChargeStore(ctx, i.usage)
		ctx = WithChargeAmount(ctx, i.chargePerRequestAmount, i.chargePerRequestUnit)
		ctx = WithChargeEventEmitter(ctx, i.emitter)
		ctx = withLastOpHolder(ctx)
		ctx = withIdempotencyKeyPresent(ctx, requestHasIdempotencyKey(req))
		return next(ctx, req)
	}
}

func (i *capabilityInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *capabilityInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		token := extractCapabilityToken(conn.RequestHeader().Get(HeaderCapability), conn.RequestHeader().Get("Authorization"))
		if token == "" {
			return next(ctx, conn)
		}
		cap, err := i.verifier.Verify(ctx, token, i.audience)
		if err != nil {
			return connect.NewError(connect.CodePermissionDenied, err)
		}
		if err := i.enforceCaveats(ctx, cap); err != nil {
			return err
		}
		ctx = WithCapability(ctx, cap)
		ctx, err = i.withCapabilityPrincipal(ctx, cap)
		if err != nil {
			return connect.NewError(connect.CodePermissionDenied, err)
		}
		ctx = WithChargeStore(ctx, i.usage)
		ctx = WithChargeAmount(ctx, i.chargePerRequestAmount, i.chargePerRequestUnit)
		ctx = WithChargeEventEmitter(ctx, i.emitter)
		ctx = withLastOpHolder(ctx)
		ctx = withIdempotencyKeyPresent(ctx, conn.RequestHeader().Get(paladin.HeaderIdempotencyKey) != "")
		return next(ctx, conn)
	}
}

// withCapabilityPrincipal derives a Principal from a verified capability and
// stamps it on the context, unless one is already there.
//
// The tenant comes from the capability's own subject, which the verifier has
// already required to be present and which the signature covers — a bearer
// cannot widen it. No roles are attached, deliberately: a capability must never
// satisfy a role-gated admin policy, and the data plane's Cedar policies gate
// on tenant membership, not roles. What the bearer may DO is decided by the
// caveats, checked in enforceCaveats before this runs.
func (i *capabilityInterceptor) withCapabilityPrincipal(
	ctx context.Context,
	cap *capability.Capability,
) (context.Context, error) {
	if !i.establishPrincipal {
		// The additive planes reach here too; the check lives inside rather
		// than at each call site so the two cannot drift apart.
		return ctx, nil
	}
	if _, err := PrincipalFromContext(ctx); err == nil {
		// Already authenticated by JWT or API token; the capability stays
		// additive so a caller who presents both is unaffected.
		return ctx, nil
	}
	if cap.Subject.TenantID == uuid.Nil {
		// The verifier rejects a tenant-less capability, so this is a
		// belt-and-braces refusal rather than an expected path: establishing a
		// principal with no tenant would produce a caller the tenant gate
		// cannot scope.
		return ctx, errors.New("capability: subject has no tenant")
	}

	return WithPrincipal(ctx, &Principal{
		TenantID: cap.Subject.TenantID,
		Subject:  "capability:" + cap.ID.String(),
		// The interceptor's audience is the capability PLANE label ("data"),
		// while RequireAudience compares against the canonical plane audience
		// ("paladin-data"). Mapping here is what the API-token path already does;
		// carrying the label through unmapped produced `token audience "data"
		// is not allowed on "paladin-data" plane` — a request that had just
		// authenticated, refused for a naming mismatch.
		Audience: principalAudienceFor(i.audience),
		Kind:     PrincipalKindCapability,
	}), nil
}

// enforceCaveats runs the per-connection caveat checks: source-IP CIDR
// match and the request-count limit. Order:
//
//  1. CIDR check first (cheap; pure in-memory match) — rejects
//     before we hit the DB.
//  2. Request-count bump for the capability and its ancestors (one
//     transaction; concurrency-safe via UPSERT-and-check). When the
//     usage store is nil, MaxRequests is a no-op even if the caveat is
//     set — the operator opted out.
//
// Per-operation caveats (op, resource, idempotency key) are checked by
// AssertCapabilityOp, where the handler names the operation.
//
// Budget enforcement is NOT done here — Charge() is per-handler,
// called after the cost-emitting work; it lives in this package as
// auth.ChargeCapability for handlers to invoke.
func (i *capabilityInterceptor) enforceCaveats(
	ctx context.Context,
	cap *capability.Capability,
) error {
	if len(cap.Caveats.SourceIPCIDR) > 0 {
		// An address the listener could not resolve is the zero netip.Addr,
		// which CheckSource refuses: "unknown" is not "inside the range".
		addr, _ := clientip.FromContext(ctx)
		if err := cap.Caveats.CheckSource(addr); err != nil {
			return connect.NewError(connect.CodePermissionDenied, err)
		}
	}

	// Bumped whenever a delegated capability is presented too, not only when
	// it carries its own MaxRequests: an ancestor's ceiling bounds the whole
	// subtree, and the store reads it from the ancestor's record.
	if i.usage != nil && (cap.Caveats.MaxRequests > 0 || cap.ParentID != uuid.Nil) {
		if _, err := i.usage.BumpRequest(ledgerContext(ctx, cap), capability.RequestBump{
			CapabilityID: cap.ID,
			TenantID:     cap.Subject.TenantID,
			MaxRequests:  int64(cap.Caveats.MaxRequests),
		}); err != nil {
			if errors.Is(err, capability.ErrRequestLimitExceeded) {
				return connect.NewError(connect.CodeResourceExhausted, err)
			}
			// DB-side error: fail closed. A capability with a
			// MaxRequests cap that can't be incremented atomically
			// is safer to reject than to allow unbounded use.
			return connect.NewError(connect.CodeUnavailable, err)
		}
	}
	return nil
}

// ledgerContext scopes a write to a capability's own ledger — its request
// counter, its spend, its tenant's budget and charges rows — to the
// capability's tenant. Those rows are RLS-isolated by that tenant, and the
// caller's context does not carry it: the request-count bump runs before the
// interceptor establishes a principal, and a capability presented alongside a
// JWT keeps the JWT's tenant. Either way the database refused the write.
//
// WithActingTenant authorises nothing, and here nothing needs authorising: the
// verifier has checked the signature that covers this tenant, and the write is
// to that capability's own accounting. Only the store call gets this context;
// the request itself stays scoped to whoever the caller is.
func ledgerContext(ctx context.Context, cap *capability.Capability) context.Context {
	return WithActingTenant(ctx, cap.Subject.TenantID)
}

// extractCapabilityToken reads the token from either of the supported
// headers. X-Paladin-Capability wins over Authorization scheme=capability
// when both are present (explicit > overloaded).
func extractCapabilityToken(xlegate, authz string) string {
	if t := strings.TrimSpace(xlegate); t != "" {
		return t
	}
	if authz == "" {
		return ""
	}
	parts := strings.SplitN(strings.TrimSpace(authz), " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], AuthorizationCapScheme) {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// chargeKey context value carries the UsageStore the handler should
// charge against. The interceptor stamps it whenever the capability
// subsystem is wired so handlers don't need a global reference.
type chargeKey struct{}

// lastOpKey carries a per-request *string that AssertCapabilityOp
// writes the most-recently-asserted capability op into, so a later
// ChargeRequest / ChargeCapability call in the same handler can
// stamp it onto the charges-ledger row without the caller threading
// the op as an extra arg.
//
// Using a pointer-to-string holder rather than a context.WithValue
// rebind keeps existing handler code shape: handlers call
// AssertCapabilityOp(ctx, op, uri) without re-binding ctx (return
// signature stays `error`, not `(context.Context, error)`). The
// interceptor allocates one holder per request and pins it in ctx
// before the handler runs; AssertCapabilityOp mutates the target.
type lastOpKey struct{}

// withLastOpHolder allocates a fresh holder and stamps it. Called
// once per request from the interceptor, before the handler runs.
// Handler-side: AssertCapabilityOp writes via stampLastOp,
// ChargeCapability reads via readLastOp.
func withLastOpHolder(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, lastChargeKey{}, new(uuid.UUID))
	return context.WithValue(ctx, lastOpKey{}, new(string))
}

// stampLastOp writes the supplied op into the per-request holder.
// No-op when no holder is installed (unit-test contexts that bypass
// the interceptor; the read side falls back to "").
func stampLastOp(ctx context.Context, op capability.Op) {
	if holder, ok := ctx.Value(lastOpKey{}).(*string); ok {
		*holder = string(op)
	}
}

// readLastOp returns the most-recently-asserted op for this request,
// or "" if AssertCapabilityOp hasn't been called yet (or the holder
// isn't installed).
func readLastOp(ctx context.Context) string {
	if holder, ok := ctx.Value(lastOpKey{}).(*string); ok && holder != nil {
		return *holder
	}
	return ""
}

// chargeAmountKey carries the per-request default charge amount +
// unit code (cfg.Capability.ChargePerRequestAmount /
// ChargePerRequestUnit) so handlers don't need to read config —
// they call auth.ChargeRequest(ctx) and the interceptor's stamp
// determines the amount and currency.
type chargeAmountKey struct{}

// chargeEventsKey carries an optional ChargeEventEmitter that
// ChargeCapability fans an `paladin.capability.charged` event through,
// enqueued on the SAME transaction as the charge so the event is
// atomic with the spend (ADR-0003 — no dual-write window). nil-safe:
// when unset (or cfg.Dispatcher.ChargeEventsEnabled = false at boot)
// the fan-out is skipped and the charge path stays counters + ledger.
//
// Pattern parallels chargeKey/chargeAmountKey: interceptor stamps
// at request time; ChargeCapability reads. Decoupled because charge
// events are high-cardinality (every chargeable RPC) and operators
// may want them on for one tenant and off for another — a future
// per-tenant override would land here without touching ChargeCapability.
type chargeEventsKey struct{}

// ChargeEventEmitter is the narrow seam ChargeCapability uses to fan
// out per-charge events. Implementations: a thin adapter over
// *worker.Dispatcher (lives in the wiring layer; can't import worker
// from auth without a cycle). Nil-safe.
//
// EmitChargedTx enqueues the event's outbox rows on `tx` — the same
// transaction the UsageStore uses for the counters + ledger row — so
// the fan-out commits atomically with the charge (or rolls back with
// it). An error propagates up and rolls the charge back.
type ChargeEventEmitter interface {
	EmitChargedTx(ctx context.Context, tx pgx.Tx, tenantID, capabilityID, op, actor string, amount float64, unitCode string) error
}

// WithChargeEventEmitter stamps the optional emitter on ctx. Wiring
// passes a real emitter only when cfg.Dispatcher.ChargeEventsEnabled
// is true; otherwise this is never called and the chargeEventsKey
// stays unset.
func WithChargeEventEmitter(ctx context.Context, e ChargeEventEmitter) context.Context {
	if e == nil {
		return ctx
	}
	return context.WithValue(ctx, chargeEventsKey{}, e)
}

// chargeEventEmitterFromContext returns the stamped emitter or nil.
// Internal — only ChargeCapability needs this.
func chargeEventEmitterFromContext(ctx context.Context) ChargeEventEmitter {
	e, _ := ctx.Value(chargeEventsKey{}).(ChargeEventEmitter)
	return e
}

// chargeAmount is the typed value behind chargeAmountKey. Bundling
// amount + unit avoids two context lookups per charge (the unit is
// always read alongside the amount).
type chargeAmount struct {
	Amount float64
	Unit   string
}

// WithChargeStore stamps the UsageStore onto a context. Wired by the
// capability interceptor at request time; tests can preset for unit
// coverage of charging handlers.
func WithChargeStore(ctx context.Context, s capability.UsageStore[pgx.Tx]) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, chargeKey{}, s)
}

// WithChargeAmount stamps the per-request default charge amount and
// unit code. Independent of WithChargeStore so a test can wire one
// without the other (e.g. verify the no-store path is a no-op).
//
// An empty unit string means "use the capability's own UnitCode at
// charge time"; the resolution happens in ChargeCapability so
// callers don't have to reach across config + capability state.
func WithChargeAmount(ctx context.Context, amount float64, unit string) context.Context {
	if amount <= 0 {
		return ctx
	}
	return context.WithValue(ctx, chargeAmountKey{}, chargeAmount{Amount: amount, Unit: unit})
}

// ChargeCapability is the handler-side spend hook. Handlers that emit
// cost (e.g. presign issuance, batch op kickoff, future LLM calls)
// call this once the work is committed:
//
//	if err := auth.ChargeCapability(ctx, 0.0001); err != nil {
//	    return nil, err
//	}
//
// Behaviour:
//
//   - No capability on context (JWT auth) → no-op, returns nil.
//   - Capability without MaxBudgetUSD AND tenant without aggregate
//     cap → records spend on both counters but never rejects
//     (operator audits via capability.UsageStore[pgx.Tx].Get / GetTenantBudget).
//   - Capability cap set and the new charge would exceed it →
//     CodeResourceExhausted; per-capability row NOT mutated so the
//     handler can decide to refund / log / retry. Tenant counter
//     also untouched.
//   - Tenant aggregate cap set and the new charge would exceed it
//     after the per-capability charge already committed → the
//     UsageStore compensates the capability counter, returns
//     CodeResourceExhausted with ErrTenantBudgetExceeded.
//   - UsageStore not wired → no-op (operator opted out).
//

// ChargeCapability is the handler-side spend hook. Handlers that emit
// cost (e.g. presign issuance, batch op kickoff, future LLM calls)
// call this once the work is committed:
//
//	if err := auth.ChargeCapability(ctx, 0.0001, "USD"); err != nil {
//	    return nil, err
//	}
//
// unit may be empty — in that case the capability's own UnitCode is
// used (defaulting to capability.DefaultUnitCode when even that is
// blank). This keeps existing callers — which only knew about USD —
// working without a per-call unit-string thread-through.
//
// Behaviour:
//
//   - No capability on context (JWT auth) → no-op, returns nil.
//   - Capability without MaxBudgetAmount AND tenant without aggregate
//     cap → records spend on both counters but never rejects
//     (operator audits via capability.UsageStore[pgx.Tx].Get / GetTenantBudget).
//   - Capability cap set and the new charge would exceed it →
//     CodeResourceExhausted; per-capability row NOT mutated so the
//     handler can decide to refund / log / retry. Tenant counter
//     also untouched.
//   - Tenant aggregate cap set and the new charge would exceed it
//     after the per-capability charge already committed → the
//     UsageStore compensates the capability counter, returns
//     CodeResourceExhausted with ErrTenantBudgetExceeded.
//   - UsageStore not wired → no-op (operator opted out).
//
// Refunds are exposed via auth.RefundLastCharge for handlers that
// detect a partial failure after the charge.
func ChargeCapability(ctx context.Context, amount float64, unit string) error {
	cap, ok := CapabilityFromContext(ctx)
	if !ok {
		return nil
	}
	store, ok := ctx.Value(chargeKey{}).(capability.UsageStore[pgx.Tx])
	if !ok || store == nil {
		return nil
	}
	if amount <= 0 {
		return nil
	}
	resolvedUnit := unit
	if resolvedUnit == "" {
		resolvedUnit = cap.Caveats.UnitCode
	}
	if resolvedUnit == "" {
		resolvedUnit = capability.DefaultUnitCode
	}
	tenantID := cap.Subject.TenantID // zero ⇒ tenant-budget path skipped
	// op + actor populate the charges ledger row (the schema baseline (001_initial_schema.sql)).
	// op is read from the per-request holder that AssertCapabilityOp
	// writes into. If the handler hasn't called AssertCapabilityOp
	// (legacy paths, JWT-only flows) op stays "" and the ledger row
	// has no op attribution — operator's "Top ops" /billing breakdown
	// will collect those into the empty-string row, which is the
	// honest answer.
	op := readLastOp(ctx)
	actor := cap.Subject.Subject
	// Optional transactional fan-out — only wired when the operator
	// enabled charge events (cfg.Dispatcher.ChargeEventsEnabled) AND
	// there's a tenant to attribute the event to. onCharged runs
	// INSIDE the charge transaction (dispatcher.DispatchTx on the same
	// tx), so the event outbox rows are atomic with the spend — no
	// dual-write window. nil ⇒ the store commits counters + ledger
	// with no fan-out.
	var onCharged func(ctx context.Context, tx pgx.Tx) error
	if emitter := chargeEventEmitterFromContext(ctx); emitter != nil && tenantID != uuid.Nil {
		onCharged = func(ctx context.Context, tx pgx.Tx) error {
			return emitter.EmitChargedTx(ctx, tx, tenantID.String(), cap.ID.String(), op, actor, amount, resolvedUnit)
		}
	}
	receipt, err := store.Charge(ledgerContext(ctx, cap), capability.ChargeRequest{
		CapabilityID: cap.ID,
		TenantID:     tenantID,
		Amount:       amount,
		MaxBudget:    cap.Caveats.MaxBudgetAmount,
		UnitCode:     resolvedUnit,
		Op:           op,
		Actor:        actor,
	}, onCharged)
	if err != nil {
		// The two exhaustion cases are separated because they need different
		// answers: a capability at its cap is reissued, a tenant at its cap
		// is a billing conversation. Collapsed into one counter they are
		// indistinguishable, and both look like "the API is rejecting us".
		switch {
		case errors.Is(err, capability.ErrBudgetExceeded):
			metrics.RecordCapabilityCharge(ctx, tenantID.String(), "capability_exhausted", 0)
			return connect.NewError(connect.CodeResourceExhausted, err)
		case errors.Is(err, capability.ErrTenantBudgetExceeded):
			metrics.RecordCapabilityCharge(ctx, tenantID.String(), "tenant_exhausted", 0)
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
		metrics.RecordCapabilityCharge(ctx, tenantID.String(), "error", 0)
		return connect.NewError(connect.CodeUnavailable, err)
	}
	metrics.RecordCapabilityCharge(ctx, tenantID.String(), "charged", amount)
	stampLastCharge(ctx, receipt.ChargeID)
	return nil
}

// ChargeRequest is the canonical post-work hook handlers call to
// burn the cfg-driven per-request budget against the active
// capability. Equivalent to ChargeCapability(ctx, amount, unit)
// where amount + unit come from cfg.Capability.ChargePerRequest*
// stamped onto the context by the interceptor.
//
// Behaviour mirrors ChargeCapability:
//
//   - No capability on context (JWT auth) → no-op.
//   - No charge amount on context (cfg.ChargePerRequestAmount = 0)
//     → no-op.
//   - Otherwise: forwards to ChargeCapability with the stamped
//     amount + unit.
//
// Charging AFTER successful work avoids burning budget on requests
// that never produced a billable artifact (denied, panicked,
// validation failure).
func ChargeRequest(ctx context.Context) error {
	amt, ok := ctx.Value(chargeAmountKey{}).(chargeAmount)
	if !ok || amt.Amount <= 0 {
		return nil
	}
	return ChargeCapability(ctx, amt.Amount, amt.Unit)
}

// RefundLastCharge returns spend from the most recent charge this request
// made (ChargeCapability / ChargeRequest) to every counter that charge
// debited: the capability's, each ancestor's and the tenant's. Use it when a
// handler detects that an already-charged operation must be rolled back
// (storage write failed after presign was issued, agent cancelled
// mid-flow), or to settle an estimate once the real cost is known.
//
// amount = 0 refunds whatever the charge has left, which makes a retried
// full refund a no-op rather than a second credit. A partial refund larger
// than what is left is refused. No-op when no capability is on context, no
// store is wired, or this request has made no charge.
func RefundLastCharge(ctx context.Context, amount float64) error {
	cap, ok := CapabilityFromContext(ctx)
	if !ok {
		return nil
	}
	store, ok := ctx.Value(chargeKey{}).(capability.UsageStore[pgx.Tx])
	if !ok || store == nil {
		return nil
	}
	chargeID := readLastCharge(ctx)
	if chargeID == uuid.Nil {
		return nil
	}
	if _, err := store.Refund(ledgerContext(ctx, cap), capability.RefundRequest{ChargeID: chargeID, Amount: amount}); err != nil {
		if errors.Is(err, capability.ErrRefundExceedsCharge) || errors.Is(err, capability.ErrInvalidAmount) {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
		return connect.NewError(connect.CodeUnavailable, err)
	}
	return nil
}

// AssertCapabilityOp is the handler-side gate. Call early in any
// handler that wants to honour capability caveats:
//
//	if err := auth.AssertCapabilityOp(ctx, capability.OpGet, key); err != nil {
//	    return nil, err
//	}
//
// Behaviour:
//
//   - No capability on context (caller used JWT auth) → returns nil.
//     Handler proceeds as before; existing role / Cedar gates still run.
//
//   - Capability present and op + resource allowed → returns nil.
//     Handler proceeds; downstream code may also call CapabilityFromContext
//     to read budget / source-IP / other caveats.
//
//   - Capability present but a caveat refuses the operation → returns a
//     CodePermissionDenied connect.Error wrapping the module's sentinel
//     (capability.ErrOpNotAllowed, ErrResourceNotAllowed, ...), so the
//     caller sees an unambiguous "your capability didn't allow this"
//     instead of falling through to a more generic 403.
//
// The checks are capability.Caveats.Check, so delegation and enforcement
// share one definition of every caveat. resourceURI is matched against
// ResourceURIs (exact) and ResourcePrefixes (at a "/" segment boundary).
// An empty resourceURI is only accepted from a capability that is not
// resource-restricted: an operation that cannot name what it touches
// cannot be shown to stay inside a restricted scope, so it fails closed.
// For an operation over a set (a listing), pass the URI prefix that bounds
// the set. Mutating ops also need an idempotency key when the capability
// requires one.
func AssertCapabilityOp(ctx context.Context, op capability.Op, resourceURI string) error {
	cap, ok := CapabilityFromContext(ctx)
	if !ok {
		return nil // no capability presented; not our gate
	}
	// No taint signal exists in this deployment yet, so ResourceTainted is
	// left false and AllowTaintedRead restricts nothing here (see BACKLOG).
	if err := cap.Caveats.Check(capability.CheckRequest{
		Op:                op,
		Resource:          resourceURI,
		HasIdempotencyKey: idempotencyKeyPresent(ctx),
	}); err != nil {
		return connect.NewError(connect.CodePermissionDenied, err)
	}
	// Stamp the op into the per-request holder so a later
	// ChargeRequest / ChargeCapability call attributes the charges-
	// ledger row to the correct op without an extra signature thread.
	// Last call wins when a handler asserts multiple ops in the same
	// request — convention is to call AssertCapabilityOp once for the
	// dominant action.
	stampLastOp(ctx, op)
	return nil
}

// idempotencyKeyCarrier is a request message that declares an
// idempotency_key field. Mirrors middleware's resolution: the header, or
// failing that the body field.
type idempotencyKeyCarrier interface {
	GetIdempotencyKey() string
}

func requestHasIdempotencyKey(req connect.AnyRequest) bool {
	if req.Header().Get(paladin.HeaderIdempotencyKey) != "" {
		return true
	}
	c, ok := req.Any().(idempotencyKeyCarrier)
	return ok && c.GetIdempotencyKey() != ""
}

type idempotencyKeyPresentKey struct{}

func withIdempotencyKeyPresent(ctx context.Context, present bool) context.Context {
	return context.WithValue(ctx, idempotencyKeyPresentKey{}, present)
}

func idempotencyKeyPresent(ctx context.Context) bool {
	present, _ := ctx.Value(idempotencyKeyPresentKey{}).(bool)
	return present
}

// lastChargeKey carries a per-request *uuid.UUID holding the ID of the
// most recent charge, so RefundLastCharge can name it. Installed with the
// other per-request holders.
type lastChargeKey struct{}

func stampLastCharge(ctx context.Context, id uuid.UUID) {
	if holder, ok := ctx.Value(lastChargeKey{}).(*uuid.UUID); ok {
		*holder = id
	}
}

func readLastCharge(ctx context.Context) uuid.UUID {
	if holder, ok := ctx.Value(lastChargeKey{}).(*uuid.UUID); ok && holder != nil {
		return *holder
	}
	return uuid.Nil
}

// passthroughInterceptor is the no-op variant returned when the
// capability subsystem is disabled. Implements connect.Interceptor by
// forwarding every callback unchanged.
type passthroughInterceptor struct{}

func (passthroughInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return next
}
func (passthroughInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}
func (passthroughInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
