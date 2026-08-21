package auth

import (
	"context"
	"errors"
	"net"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	HeaderCapability       = "X-Paladin-Capability"
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
// realIPHeader names the proxy header that carries the client IP
// for SourceIPCIDR enforcement (e.g. "X-Forwarded-For"). Empty means
// "fall back to req.Header().Get('X-Real-Ip') or skip CIDR check
// entirely". The chart's HTTPServer config already pins the
// trusted-proxy header per plane; this value mirrors it.
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
	realIPHeader string,
	chargePerRequestAmount float64,
	chargePerRequestUnit string,
) connect.Interceptor {
	return CapabilityInterceptorWithEvents(verifier, audience, usage, realIPHeader,
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
	realIPHeader string,
	chargePerRequestAmount float64,
	chargePerRequestUnit string,
	emitter ChargeEventEmitter,
) connect.Interceptor {
	if verifier == nil {
		return passthroughInterceptor{}
	}
	if realIPHeader == "" {
		realIPHeader = "X-Forwarded-For"
	}
	return &capabilityInterceptor{
		verifier:               verifier,
		audience:               audience,
		usage:                  usage,
		realIPHeader:           realIPHeader,
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
	realIPHeader string,
	chargePerRequestAmount float64,
	chargePerRequestUnit string,
	emitter ChargeEventEmitter,
) connect.Interceptor {
	i := CapabilityInterceptorWithEvents(verifier, audience, usage, realIPHeader,
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
	realIPHeader           string
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
		if err := i.enforceCaveats(ctx, cap, req.Header()); err != nil {
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
		if err := i.enforceCaveats(ctx, cap, conn.RequestHeader()); err != nil {
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

// enforceCaveats runs the runtime-bound caveat checks: source-IP CIDR
// match and per-capability request-count limit. Order:
//
//  1. CIDR check first (cheap; pure in-memory match) — rejects
//     before we hit the DB.
//  2. Request-count bump (one round-trip; concurrency-safe via
//     UPSERT-and-check). When usage store is nil, MaxRequests is a
//     no-op even if the caveat is set — the operator opted out.
//
// Budget enforcement is NOT done here — Charge() is per-handler,
// called after the cost-emitting work; it lives in this package as
// auth.ChargeCapability for handlers to invoke.
func (i *capabilityInterceptor) enforceCaveats(
	ctx context.Context,
	cap *capability.Capability,
	header HeaderGetter,
) error {
	if len(cap.Caveats.SourceIPCIDR) > 0 {
		clientIP := i.clientIP(header)
		if clientIP == nil {
			return connect.NewError(connect.CodePermissionDenied,
				errors.New("capability: SourceIPCIDR set but client IP unknown"))
		}
		if !ipInAnyCIDR(clientIP, cap.Caveats.SourceIPCIDR) {
			return connect.NewError(connect.CodePermissionDenied,
				errors.New("capability: client IP not in SourceIPCIDR allow-list"))
		}
	}

	if i.usage != nil && cap.Caveats.MaxRequests > 0 {
		if _, err := i.usage.BumpRequest(ctx, cap.ID, int64(cap.Caveats.MaxRequests)); err != nil {
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

// HeaderGetter is the read-only header surface both connect.AnyRequest
// and connect.StreamingHandlerConn expose. Local interface so
// enforceCaveats accepts either without coupling to a specific
// connect type.
type HeaderGetter interface {
	Get(key string) string
}

// clientIP extracts the caller's IP. Honours the configured proxy
// header (X-Forwarded-For by default, leftmost client). Falls back
// to X-Real-Ip. Returns nil when neither header is present —
// SourceIPCIDR enforcement upstream rejects in that case.
func (i *capabilityInterceptor) clientIP(header HeaderGetter) net.IP {
	if v := header.Get(i.realIPHeader); v != "" {
		// X-Forwarded-For format: "client, proxy1, proxy2". Leftmost
		// non-empty entry is the client. Trim spaces; tolerate the
		// "X-Real-Ip"-style single-value form too.
		first := v
		if idx := strings.IndexByte(v, ','); idx > 0 {
			first = v[:idx]
		}
		if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
			return ip
		}
	}
	if v := header.Get("X-Real-Ip"); v != "" {
		if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
			return ip
		}
	}
	return nil
}

// ipInAnyCIDR returns true when ip falls inside at least one CIDR
// from the supplied list. Invalid CIDRs are skipped (delegation
// narrowing already rejects them at issue time, but the verifier
// path is defensive).
func ipInAnyCIDR(ip net.IP, cidrs []string) bool {
	for _, c := range cidrs {
		_, network, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
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
	holder := new(string)
	return context.WithValue(ctx, lastOpKey{}, holder)
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
// Refunds are exposed via auth.RefundCapability for handlers that
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
	_, err := store.Charge(ctx, cap.ID, amount, cap.Caveats.MaxBudgetAmount, resolvedUnit, tenantID, op, actor, onCharged)
	if err != nil {
		if errors.Is(err, capability.ErrBudgetExceeded) ||
			errors.Is(err, capability.ErrTenantBudgetExceeded) {
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
		return connect.NewError(connect.CodeUnavailable, err)
	}
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

// RefundCapability subtracts amount from the per-capability spend
// AND the tenant aggregate. Use it when a handler detects that an
// already-charged operation must be rolled back (storage write
// failed after presign was issued, agent cancelled mid-flow).
//
// Idempotent on both counters — flooring at 0 means a double-refund
// doesn't go negative. No-op when no capability is on context, no
// store wired, or amount <= 0.
//
// Currency-naive: refunds the same numeric value off whatever
// counter exists. Both counters are pinned to the same unit (the
// capability's UnitCode), so the refund always cancels the right
// quantity.
func RefundCapability(ctx context.Context, amount float64) error {
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
	if err := store.RefundCapability(ctx, cap.ID, amount); err != nil {
		return connect.NewError(connect.CodeUnavailable, err)
	}
	if cap.Subject.TenantID != uuid.Nil {
		if err := store.RefundTenant(ctx, cap.Subject.TenantID, amount); err != nil {
			return connect.NewError(connect.CodeUnavailable, err)
		}
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
//   - Capability present but op not in Caveats.Ops, or resource not
//     under any of Caveats.ResourcePrefixes / ResourceURIs → returns a
//     CodePermissionDenied connect.Error so the caller sees an
//     unambiguous "your capability didn't allow this" instead of
//     falling through to a more generic 403.
//
// resourceURI is matched as a string against ResourcePrefixes (HasPrefix)
// and ResourceURIs (exact). Empty resourceURI skips the resource check —
// useful for ops that don't target a specific URI (e.g. capability self-
// introspect).
func AssertCapabilityOp(ctx context.Context, op capability.Op, resourceURI string) error {
	cap, ok := CapabilityFromContext(ctx)
	if !ok {
		return nil // no capability presented; not our gate
	}
	if !containsOp(cap.Caveats.Ops, op) {
		return connect.NewError(connect.CodePermissionDenied,
			capabilityOpNotAllowed{op: op, allowed: cap.Caveats.Ops})
	}
	if resourceURI != "" && !resourceAllowed(cap.Caveats, resourceURI) {
		return connect.NewError(connect.CodePermissionDenied,
			capabilityResourceNotAllowed{uri: resourceURI})
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

// containsOp checks Op set membership without dragging slices.Contains
// into every call site.
func containsOp(set []capability.Op, want capability.Op) bool {
	for _, op := range set {
		if op == want {
			return true
		}
	}
	return false
}

// resourceAllowed reports whether the supplied URI is reachable under
// the capability's resource caveats. Empty caveats = unrestricted within
// tenant scope (the verifier already enforced that).
func resourceAllowed(c capability.Caveats, uri string) bool {
	if len(c.ResourcePrefixes) == 0 && len(c.ResourceURIs) == 0 {
		return true
	}
	for _, exact := range c.ResourceURIs {
		if exact == uri {
			return true
		}
	}
	for _, prefix := range c.ResourcePrefixes {
		if len(prefix) > 0 && len(uri) >= len(prefix) && uri[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// capabilityOpNotAllowed and capabilityResourceNotAllowed are typed
// errors so callers / tests can branch on the rejection reason without
// string-matching connect error messages.
type capabilityOpNotAllowed struct {
	op      capability.Op
	allowed []capability.Op
}

func (e capabilityOpNotAllowed) Error() string {
	return "capability op " + string(e.op) + " not in allowed set"
}

type capabilityResourceNotAllowed struct {
	uri string
}

func (e capabilityResourceNotAllowed) Error() string {
	return "capability does not authorise resource " + e.uri
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
