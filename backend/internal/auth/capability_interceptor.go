package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"google.golang.org/protobuf/proto"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/dpop"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/capop"
	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
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

// capabilityKey is the context value the interceptor stashes the verified
// capability under. Read via CapabilityFromContext and
// VerifiedCapabilityFromContext.
type capabilityKey struct{}

// verifiedEntry is what capabilityKey holds: the capability as the verifier
// accepted it — the only source of what the Meter counts against — and the
// capability it grants, read out once.
type verifiedEntry struct {
	verified limes.VerifiedCapability
	cap      *limes.Capability
}

// CapabilityFromContext returns the verified capability for the request,
// or nil when none was supplied / verified. Handlers gate on caveats
// (op set, resource prefix, budget) by calling this and switching on
// the result.
// errChargeWithoutTx is a charge whose store put no transaction in the
// context onCharged receives: the charge events could not join it, so the
// charge is refused rather than written without them.
var errChargeWithoutTx = errors.New("capability: the usage store passed no transaction to onCharged")

func CapabilityFromContext(ctx context.Context) (*limes.Capability, bool) {
	e, ok := ctx.Value(capabilityKey{}).(verifiedEntry)
	return e.cap, ok && e.cap != nil
}

// VerifiedCapabilityFromContext returns the capability as the verifier
// accepted it: what metering counts against, and what delegation and a
// Biscuit are minted from.
func VerifiedCapabilityFromContext(ctx context.Context) (limes.VerifiedCapability, bool) {
	e, ok := ctx.Value(capabilityKey{}).(verifiedEntry)
	return e.verified, ok && e.cap != nil
}

// WithCapability stamps a verified capability onto a context. Public so
// tests can prepare contexts without going through the interceptor; only a
// limes.Verifier makes a VerifiedCapability, so a test verifies a token too.
func WithCapability(ctx context.Context, v limes.VerifiedCapability) context.Context {
	c := v.Capability()
	if c.ID == uuid.Nil() {
		return ctx // the zero VerifiedCapability names no capability
	}
	return context.WithValue(ctx, capabilityKey{}, verifiedEntry{verified: v, cap: &c})
}

// CapabilityInterceptor is an *additive* Connect interceptor — it
// looks for a capability token on the inbound request and, when present
// and valid, stamps the verified *limes.Capability onto the
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
// carries a request ceiling, every request bumps the counter and rejects
// when over. When nil, the ceiling is silently un-enforced — the operator
// opted out by not wiring the store.
//
// SourceIPCIDR is checked against the client address the listener resolved
// from its trusted proxies (clientip.Middleware), never against a forwarding
// header directly: the leftmost entry of one is whatever the caller wrote.
//
// chargePerRequestAmount is the auto-charge stamped onto the context. It is
// denominated in each capability's own unit, which its token pins: at
// handler time, ChargeRequest passes it into UsageStore.Charge.
func CapabilityInterceptor(
	verifier *limes.Verifier,
	audience string,
	usage meter.UsageStore,
	chargePerRequestAmount limes.Nanos,
	opts ...CapabilityOption,
) connect.ServerInterceptor {
	return CapabilityInterceptorWithEvents(verifier, audience, usage,
		chargePerRequestAmount, nil, opts...)
}

// CapabilityOption configures a capability interceptor.
type CapabilityOption func(*capabilityInterceptor)

// CapabilityInterceptorWithEvents is the events-aware variant. The
// emitter is stamped on every authenticated request's context so
// ChargeCapability can fan out an `paladin.capability.charged` event AFTER
// the running totals commit. nil emitter = no events (the default —
// gated on cfg.Dispatcher.ChargeEventsEnabled at the wiring layer).
func CapabilityInterceptorWithEvents(
	verifier *limes.Verifier,
	audience string,
	usage meter.UsageStore,
	chargePerRequestAmount limes.Nanos,
	emitter ChargeEventEmitter,
	opts ...CapabilityOption,
) connect.ServerInterceptor {
	ci := newCapabilityInterceptor(verifier, audience, usage, chargePerRequestAmount, emitter, opts...)
	if ci == nil {
		return passthrough
	}
	return ci.interceptor()
}

// newCapabilityInterceptor is nil when there is no verifier: the capability
// subsystem is off.
func newCapabilityInterceptor(
	verifier *limes.Verifier,
	audience string,
	usage meter.UsageStore,
	chargePerRequestAmount limes.Nanos,
	emitter ChargeEventEmitter,
	opts ...CapabilityOption,
) *capabilityInterceptor {
	if verifier == nil {
		return nil
	}
	ci := &capabilityInterceptor{
		verifier:               verifier,
		audience:               audience,
		usage:                  usage,
		chargePerRequestAmount: chargePerRequestAmount,
		emitter:                emitter,
	}
	for _, o := range opts {
		o(ci)
	}
	return ci
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
	verifier *limes.Verifier,
	audience string,
	usage meter.UsageStore,
	chargePerRequestAmount limes.Nanos,
	emitter ChargeEventEmitter,
	opts ...CapabilityOption,
) connect.ServerInterceptor {
	ci := newCapabilityInterceptor(verifier, audience, usage, chargePerRequestAmount, emitter, opts...)
	if ci == nil {
		return passthrough
	}
	ci.establishPrincipal = true
	return ci.interceptor()
}

// TaintLookup reports whether the object a capability resource URI names
// has been flagged with any taint signal. A URI that names no object is not
// tainted.
type TaintLookup func(ctx context.Context, resourceURI string) (bool, error)

// WithTaintLookup turns on the AllowTaintedRead caveat: a capability without
// it is refused a read of an object the lookup reports tainted. Without a
// lookup the caveat cannot act — there is no taint signal to read.
func WithTaintLookup(lookup TaintLookup) CapabilityOption {
	return func(i *capabilityInterceptor) { i.taintLookup = lookup }
}

type capabilityInterceptor struct {
	verifier *limes.Verifier
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
	usage                  meter.UsageStore
	chargePerRequestAmount limes.Nanos
	emitter                ChargeEventEmitter
	taintLookup            TaintLookup
}

// interceptor verifies the capability a call carries. A unary call's
// idempotency key may be in its request message as well as its header, so
// the unary path reads the message; a stream has only the header.
func (i *capabilityInterceptor) interceptor() connect.ServerInterceptor {
	return unary.Interceptor(
		func(next unary.Func) unary.Func {
			return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
				header := unary.Info(ctx).RequestHeader()
				ctx, err := i.establish(ctx, spec, header, requestHasIdempotencyKey(header, req))
				if err != nil {
					return nil, err
				}
				return next(ctx, spec, req)
			}
		},
		func(next connect.ServerFunc) connect.ServerFunc {
			return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
				header := unary.Info(ctx).RequestHeader()
				ctx, err := i.establish(ctx, spec, header, header.Get(paladin.HeaderIdempotencyKey) != "")
				if err != nil {
					return err
				}
				return next(ctx, spec, stream)
			}
		},
	)
}

// establish verifies the call's capability, if it carries one, and returns
// the context the call runs under.
func (i *capabilityInterceptor) establish(ctx context.Context, spec connect.Spec, header *connect.Header, hasKey bool) (context.Context, error) {
	token, scheme := extractCapabilityToken(header.Get(HeaderCapability), header.Get("Authorization"))
	if token == "" {
		return ctx, nil
	}
	// Verify, not Authorize: the operation, the resource and the taint flag
	// are known only to the handler, which checks them (ADR-0031).
	//
	// The verifier checks a key-bound capability's DPoP proof itself, with
	// the proof checker it was built with; one it has none for is refused.
	// The URL is the procedure path: behind a proxy the server cannot know
	// the scheme and host its clients address it by, and the paths of
	// different services never coincide.
	verified, err := i.verifier.Verify(ctx, limes.VerifyRequest{
		Token:    token,
		Audience: i.audience,
		Scheme:   scheme,
		DPoP: dpop.Proof{
			JWT:    header.Get(paladin.HeaderDPoP),
			Method: httpMethod(ctx),
			URL:    spec.Procedure,
		},
	})
	if err != nil {
		// Token was supplied and not accepted. Falling through to JWT
		// silently would mask the misconfiguration.
		if errors.Is(err, dpop.ErrNonceRequired) {
			return ctx, i.nonceChallenge(ctx, err, scheme)
		}
		return ctx, verifyError(ctx, err)
	}
	ctx = WithCapability(ctx, verified)
	cap, _ := CapabilityFromContext(ctx)
	if cap.ConfirmationJKT != "" {
		i.sendNonce(ctx)
	}
	if err := i.enforceCaveats(ctx, verified); err != nil {
		return ctx, err
	}
	ctx, err = i.withCapabilityPrincipal(ctx, cap)
	if err != nil {
		return ctx, capabilityError(connect.CodePermissionDenied, err)
	}
	ctx = WithChargeStore(ctx, i.usage)
	ctx = WithChargeAmount(ctx, i.chargePerRequestAmount)
	ctx = WithChargeEventEmitter(ctx, i.emitter)
	ctx = withLastOpHolder(ctx)
	ctx = withIdempotencyKeyPresent(ctx, hasKey)
	ctx = withTaintLookup(ctx, i.taintLookup)
	return ctx, nil
}

// nonceChallenge answers a proof without the nonce the server asks for as
// RFC 9449 §8 does: Unauthenticated, a DPoP challenge naming use_dpop_nonce,
// and the nonce to sign the next proof with. limes spells the challenge; the
// refusal's reason says the proof can be signed afresh.
func (i *capabilityInterceptor) nonceChallenge(ctx context.Context, err error, scheme limes.AuthScheme) error {
	resp := i.verifier.ErrorResponse(ctx, err, scheme)
	header := unary.Info(ctx).ResponseHeader()
	for _, challenge := range resp.Challenges {
		header.Add(paladin.HeaderWWWAuthenticate, challenge)
	}
	if resp.DPoPNonce != "" {
		header.Set(dpop.NonceHeader, resp.DPoPNonce)
	}
	return capabilityError(connect.CodeUnauthenticated, err)
}

// sendNonce hands a key-bound capability's holder the nonce for its next
// proof with every response, so a client that keeps it is challenged only
// when a nonce lapses. Without nonces configured there is none to send; a
// nonce that cannot be made is left out, and the next proof is challenged.
func (i *capabilityInterceptor) sendNonce(ctx context.Context) {
	nonce, err := i.verifier.DPoPNonce(ctx)
	if err != nil {
		logger.FromContext(ctx).Warn("capability: DPoP nonce not issued", zap.Error(err))
		return
	}
	if nonce != "" {
		unary.Info(ctx).ResponseHeader().Set(dpop.NonceHeader, nonce)
	}
}

// httpMethod is the method the call came in with: POST, or GET for a
// Connect GET of a side-effect-free call.
func httpMethod(ctx context.Context) string {
	if info, ok := connecthttp.ServerInfoForContext(ctx); ok && info.HTTPMethod() != "" {
		return info.HTTPMethod()
	}
	return http.MethodPost
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
	cap *limes.Capability,
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
	if cap.Subject.TenantID == uuid.Nil() {
		// The verifier rejects a tenant-less capability, so this is a
		// belt-and-braces refusal rather than an expected path: establishing a
		// principal with no tenant would produce a caller the tenant gate
		// cannot scope.
		return ctx, errors.New("capability: subject has no tenant")
	}

	return WithPrincipal(ctx, &Principal{
		TenantID: cap.Subject.TenantID,
		Subject:  SubjectPrefixCapability + cap.ID.String(),
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
//     usage store is nil, the request ceiling is a no-op even if the
//     caveat sets one — the operator opted out.
//
// Per-operation caveats (op, resource, idempotency key) are checked by
// AssertCapabilityOp, where the handler names the operation.
//
// Budget enforcement is NOT done here — Charge() is per-handler,
// called after the cost-emitting work; it lives in this package as
// auth.ChargeCapability for handlers to invoke.
func (i *capabilityInterceptor) enforceCaveats(
	ctx context.Context,
	verified limes.VerifiedCapability,
) error {
	cap := verified.Capability()
	if len(cap.Caveats.SourceIPCIDR) > 0 {
		// An address the listener could not resolve is the zero netip.Addr,
		// which CheckSource refuses: "unknown" is not "inside the range".
		addr, _ := clientip.FromContext(ctx)
		if err := cap.Caveats.CheckSource(addr); err != nil {
			return capabilityError(connect.CodePermissionDenied, err)
		}
	}

	// Bumped whenever a delegated capability is presented too, not only when
	// it carries a request ceiling of its own: an ancestor's ceiling bounds
	// the whole subtree, and the store reads it from the ancestor's record.
	// A Biscuit copy with limits of its own is counted the same way.
	subject := verified.MeterSubject()
	if i.usage != nil && (!subject.Requests().Unlimited || cap.ParentID != uuid.Nil() || len(subject.Copies()) > 0) {
		if _, err := i.usage.Bump(ledgerContext(ctx, &cap), meter.BumpRequest{Subject: subject}); err != nil {
			// A store failure fails closed, as Unavailable: a capability
			// with a request ceiling that can't be incremented atomically
			// is safer to reject than to allow unbounded use.
			return meterError(err)
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
func ledgerContext(ctx context.Context, cap *limes.Capability) context.Context {
	return WithActingTenant(ctx, cap.Subject.TenantID)
}

// extractCapabilityToken reads the token from either of the supported
// headers, and the Authorization scheme it came under. X-Paladin-Capability
// wins over Authorization when both are present (explicit > overloaded).
// Paladin's own "capability" scheme and the header name no RFC scheme, so the
// verifier checks none for them; "DPoP" is RFC 9449's.
func extractCapabilityToken(xlegate, authz string) (string, limes.AuthScheme) {
	if t := strings.TrimSpace(xlegate); t != "" {
		return t, limes.AuthSchemeUnspecified
	}
	if authz == "" {
		return "", limes.AuthSchemeUnspecified
	}
	parts := strings.SplitN(strings.TrimSpace(authz), " ", 2)
	if len(parts) != 2 {
		return "", limes.AuthSchemeUnspecified
	}
	switch {
	case strings.EqualFold(parts[0], AuthorizationCapScheme):
		return strings.TrimSpace(parts[1]), limes.AuthSchemeUnspecified
	// RFC 9449's own scheme, for a key-bound capability: the verifier then
	// holds the token to its binding — a bearer one under DPoP is refused.
	case strings.EqualFold(parts[0], string(limes.AuthSchemeDPoP)):
		return strings.TrimSpace(parts[1]), limes.AuthSchemeDPoP
	}
	return "", limes.AuthSchemeUnspecified
}

// chargeKey context value carries the UsageStore the handler should
// charge against. The interceptor stamps it whenever the capability
// subsystem is wired so handlers don't need a global reference.
type chargeKey struct{}

// withLastOpHolder installs the per-request holders a handler's charge
// reads: the last charge id, and the op AssertCapabilityOp admitted
// (capop, which the policy engine also reads as context.capability_op).
// Called once per request from the interceptor, before the handler runs.
func withLastOpHolder(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, lastChargeKey{}, new(uuid.UUID))
	return capop.WithHolder(ctx)
}

// stampLastOp records op as the one the caveats admitted for this request.
func stampLastOp(ctx context.Context, op limes.Op) {
	capop.Stamp(ctx, string(op))
}

// readLastOp returns the most-recently-asserted op for this request, or ""
// if AssertCapabilityOp hasn't been called yet (or no holder is installed).
func readLastOp(ctx context.Context) limes.Op {
	return limes.Op(capop.Admitted(ctx))
}

// chargeAmountKey carries the per-request default charge amount
// (cfg.Capability.ChargePerRequestAmount) so handlers don't need to read
// config — they call auth.ChargeRequest(ctx) and the interceptor's stamp
// determines the amount, in the capability's own unit.
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
	EmitChargedTx(ctx context.Context, tx pgx.Tx, tenantID, capabilityID, op, actor string, amount limes.Nanos, unitCode string) error
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

// chargeAmount is the typed value behind chargeAmountKey.
type chargeAmount struct {
	Amount limes.Nanos
}

// WithChargeStore stamps the UsageStore onto a context. Wired by the
// capability interceptor at request time; tests can preset for unit
// coverage of charging handlers.
func WithChargeStore(ctx context.Context, s meter.UsageStore) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, chargeKey{}, s)
}

// WithChargeAmount stamps the per-request default charge amount, in the
// capability's own unit. Independent of WithChargeStore so a test can wire
// one without the other (e.g. verify the no-store path is a no-op).
func WithChargeAmount(ctx context.Context, amount limes.Nanos) context.Context {
	if amount <= 0 {
		return ctx
	}
	return context.WithValue(ctx, chargeAmountKey{}, chargeAmount{Amount: amount})
}

// ChargeCapability is the handler-side spend hook. Handlers that emit
// cost (e.g. presign issuance, batch op kickoff, future LLM calls)
// call this once the work is committed:
//
//	if err := auth.ChargeCapability(ctx, limes.MustParseAmount("0.0001")); err != nil {
//	    return nil, err
//	}
//
// The amount is in the capability's own unit, which its token pins: the
// Meter counts a capability in no other.
//
// Behaviour:
//
//   - No capability on context (JWT auth) → no-op, returns nil.
//   - Capability with an unlimited budget AND tenant without aggregate
//     cap → records spend on both counters but never rejects
//     (operator audits via meter.UsageStore.GetUsage / GetTenantBudget).
//   - Capability, ancestor or tenant ceiling crossed → CodeResourceExhausted;
//     nothing is mutated, so the handler can decide to refund / log / retry.
//   - UsageStore not wired → no-op (operator opted out).
//
// Refunds are exposed via auth.RefundLastCharge for handlers that
// detect a partial failure after the charge.
func ChargeCapability(ctx context.Context, amount limes.Nanos) error {
	verified, ok := VerifiedCapabilityFromContext(ctx)
	if !ok {
		return nil
	}
	cap, _ := CapabilityFromContext(ctx)
	store, ok := ctx.Value(chargeKey{}).(meter.UsageStore)
	if !ok || store == nil {
		return nil
	}
	if amount <= 0 {
		return nil
	}
	subject := verified.MeterSubject()
	tenantID := subject.TenantID()
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
	// enabled charge events (cfg.Dispatcher.ChargeEventsEnabled).
	// onCharged runs INSIDE the charge transaction (dispatcher.DispatchTx
	// on the same tx), so the event outbox rows are atomic with the spend —
	// no dual-write window. nil ⇒ the store commits counters + ledger
	// with no fan-out.
	var onCharged func(ctx context.Context, c meter.Charged) error
	if emitter := chargeEventEmitterFromContext(ctx); emitter != nil {
		onCharged = func(ctx context.Context, _ meter.Charged) error {
			// The store hands its transaction over in the context.
			tx, ok := pgstore.TxFrom(ctx)
			if !ok {
				return errChargeWithoutTx
			}
			return emitter.EmitChargedTx(ctx, tx, tenantID.String(), cap.ID.String(), string(op), actor, amount, subject.UnitCode())
		}
	}
	receipt, err := store.Charge(ledgerContext(ctx, cap), meter.ChargeRequest{
		Subject: subject,
		Amount:  amount,
		Op:      op,
		Actor:   actor,
	}, onCharged)
	if err != nil {
		// The store is otelmetric's, which counts the attempt by outcome —
		// a capability at its cap apart from a tenant at its cap.
		return meterError(err)
	}
	metrics.RecordCapabilityChargeAmount(ctx, tenantID.String(), amount.Float64())
	stampLastCharge(ctx, receipt.ChargeID)
	return nil
}

// ChargeRequest is the canonical post-work hook handlers call to
// burn the cfg-driven per-request budget against the active
// capability. Equivalent to ChargeCapability(ctx, amount) where amount
// comes from cfg.Capability.ChargePerRequestAmount stamped onto the
// context by the interceptor.
//
// Behaviour mirrors ChargeCapability:
//
//   - No capability on context (JWT auth) → no-op.
//   - No charge amount on context (cfg.ChargePerRequestAmount = 0)
//     → no-op.
//   - Otherwise: forwards to ChargeCapability with the stamped
//     amount.
//
// Charging AFTER successful work avoids burning budget on requests
// that never produced a billable artifact (denied, panicked,
// validation failure).
func ChargeRequest(ctx context.Context) error {
	amt, ok := ctx.Value(chargeAmountKey{}).(chargeAmount)
	if !ok || amt.Amount <= 0 {
		return nil
	}
	return ChargeCapability(ctx, amt.Amount)
}

// RefundLastCharge returns spend from the most recent charge this request
// made (ChargeCapability / ChargeRequest) to every counter that charge
// debited: the capability's, each ancestor's and the tenant's. Use it when a
// handler detects that an already-charged operation must be rolled back
// (storage write failed after presign was issued, agent cancelled
// mid-flow), or to settle an estimate once the real cost is known.
//
// amount = 0 refunds whatever the charge has left (RefundRequest.Remaining),
// which makes a retried full refund a no-op rather than a second credit. A
// partial refund larger than what is left is refused. No-op when no
// capability is on context, no store is wired, or this request has made no
// charge.
func RefundLastCharge(ctx context.Context, amount limes.Nanos) error {
	cap, ok := CapabilityFromContext(ctx)
	if !ok {
		return nil
	}
	store, ok := ctx.Value(chargeKey{}).(meter.UsageStore)
	if !ok || store == nil {
		return nil
	}
	chargeID := readLastCharge(ctx)
	if chargeID == uuid.Nil() {
		return nil
	}
	req := meter.RefundRequest{ChargeID: chargeID, Amount: amount}
	if amount == 0 {
		req = meter.RefundRequest{ChargeID: chargeID, Remaining: true}
	}
	if _, err := store.Refund(ledgerContext(ctx, cap), req); err != nil {
		if errors.Is(err, meter.ErrRefundExceedsCharge) {
			return capabilityError(connect.CodeInvalidArgument, err)
		}
		return meterError(err)
	}
	return nil
}

// ReserveCapability holds amount against the request's capability, its
// ancestors and its tenant before a cost is known — an LLM call priced by
// the tokens it ends up using. Settle the hold with SettleReservation once
// the actual cost is known, or ReleaseReservation if the work is abandoned;
// a hold never settled lapses on its own after ttl (0 = the module default).
//
// Returns uuid.Nil and no error when there is nothing to hold against: no
// capability on the context, or no store wired.
func ReserveCapability(ctx context.Context, amount limes.Nanos, ttl time.Duration) (uuid.UUID, error) {
	verified, ok := VerifiedCapabilityFromContext(ctx)
	if !ok {
		return uuid.Nil(), nil
	}
	cap, _ := CapabilityFromContext(ctx)
	store, ok := ctx.Value(chargeKey{}).(meter.UsageStore)
	if !ok || store == nil {
		return uuid.Nil(), nil
	}
	r, err := store.Reserve(ledgerContext(ctx, cap), meter.ReserveRequest{
		Subject: verified.MeterSubject(),
		Amount:  amount,
		TTL:     ttl,
		Op:      readLastOp(ctx),
		Actor:   cap.Subject.Subject,
	})
	if err != nil {
		return uuid.Nil(), meterError(err)
	}
	return r.ID, nil
}

// SettleReservation charges the actual cost in place of a hold made by
// ReserveCapability. A cost above the hold must fit the ceilings; if it
// does not, the hold stays and the call returns ResourceExhausted.
func SettleReservation(ctx context.Context, reservationID uuid.UUID, amount limes.Nanos) error {
	verified, ok := VerifiedCapabilityFromContext(ctx)
	if !ok || reservationID == uuid.Nil() {
		return nil
	}
	cap, _ := CapabilityFromContext(ctx)
	store, ok := ctx.Value(chargeKey{}).(meter.UsageStore)
	if !ok || store == nil {
		return nil
	}
	receipt, err := store.Settle(ledgerContext(ctx, cap), meter.SettleRequest{
		Subject:       verified.MeterSubject(),
		ReservationID: reservationID,
		Amount:        amount,
	}, nil)
	if err != nil {
		if errors.Is(err, meter.ErrReservationNotFound) {
			return capabilityError(connect.CodeFailedPrecondition, err)
		}
		return meterError(err)
	}
	stampLastCharge(ctx, receipt.ChargeID)
	return nil
}

// ReleaseReservation drops a hold made by ReserveCapability without
// charging. Idempotent. It names the capability releasing, as Settle does: the
// store releases a hold only for the subject that made it.
func ReleaseReservation(ctx context.Context, reservationID uuid.UUID) error {
	verified, ok := VerifiedCapabilityFromContext(ctx)
	if !ok || reservationID == uuid.Nil() {
		return nil
	}
	cap, _ := CapabilityFromContext(ctx)
	store, ok := ctx.Value(chargeKey{}).(meter.UsageStore)
	if !ok || store == nil {
		return nil
	}
	if err := store.Release(ledgerContext(ctx, cap), meter.ReleaseRequest{
		Subject:       verified.MeterSubject(),
		ReservationID: reservationID,
	}); err != nil {
		return meterError(err)
	}
	return nil
}

// meterError maps a metering refusal to its transport status, by the limes
// family it belongs to, so a refusal no retry mends never reads as one to
// retry:
//
//   - a limit exhausted — the capability's requests, budget or copies, or its
//     tenant's budget — is ResourceExhausted;
//   - a capability revoked or expired, or its tenant erased, is
//     PermissionDenied with its reason, as a verifier's refusal is;
//   - a malformed request is InvalidArgument;
//   - a unit other than the counters', a counter with no room and a record
//     no longer there are FailedPrecondition;
//   - a request that disagrees with what is on record — a reservation settled
//     for another amount, an external reference naming another charge — is
//     Aborted;
//   - anything else is the store failing, Unavailable.
//
// ErrUnitCodeMismatch is an ErrConflict too, so it is matched first.
func meterError(err error) error {
	switch {
	case errors.Is(err, limes.ErrRequestLimitExceeded),
		errors.Is(err, limes.ErrBudgetExceeded),
		errors.Is(err, limes.ErrTenantBudgetExceeded):
		return capabilityError(connect.CodeResourceExhausted, err)
	case errors.Is(err, limes.ErrRevoked), errors.Is(err, limes.ErrExpired):
		return capabilityError(connect.CodePermissionDenied, err)
	case errors.Is(err, limes.ErrInvalidRequest):
		return capabilityError(connect.CodeInvalidArgument, err)
	case errors.Is(err, limes.ErrUnitCodeMismatch),
		errors.Is(err, meter.ErrCounterOverflow),
		errors.Is(err, limes.ErrNotFound):
		return capabilityError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, limes.ErrConflict):
		return capabilityError(connect.CodeAborted, err)
	}
	return capabilityError(connect.CodeUnavailable, err)
}

// AssertCapabilityOp is the handler-side gate. Call early in any
// handler that wants to honour capability caveats:
//
//	if err := auth.AssertCapabilityOp(ctx, limes.OpGet, key); err != nil {
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
//     (limes.ErrOpNotAllowed, ErrResourceNotAllowed, ...), so the
//     caller sees an unambiguous "your capability didn't allow this"
//     instead of falling through to a more generic 403.
//
// The checks are limes.Caveats.Check, so delegation and enforcement
// share one definition of every caveat. resourceURI is matched against
// ResourceURIs (exact) and ResourcePrefixes (at a "/" segment boundary).
// An empty resourceURI is only accepted from a capability that is not
// resource-restricted: an operation that cannot name what it touches
// cannot be shown to stay inside a restricted scope, so it fails closed.
// For an operation over a set (a listing), pass the URI prefix that bounds
// the set. Mutating ops also need an idempotency key when the capability
// requires one.
func AssertCapabilityOp(ctx context.Context, op limes.Op, resourceURI string) error {
	return assertCapabilityOp(ctx, op, resourceURI, func(ctx context.Context) (bool, error) {
		lookup := taintLookupFrom(ctx)
		if lookup == nil {
			return false, nil
		}
		return lookup(ctx, resourceURI)
	})
}

// AssertCapabilityOpOnObject is AssertCapabilityOp for an object the caller
// has already read: tainted is whether it carries any taint signal, so a
// handler checking many objects does not look each one up again.
func AssertCapabilityOpOnObject(ctx context.Context, op limes.Op, resourceURI string, tainted bool) error {
	return assertCapabilityOp(ctx, op, resourceURI, func(context.Context) (bool, error) {
		return tainted, nil
	})
}

// assertCapabilityOp is AssertCapabilityOp with the taint source given.
func assertCapabilityOp(ctx context.Context, op limes.Op, resourceURI string, tainted func(context.Context) (bool, error)) error {
	cap, ok := CapabilityFromContext(ctx)
	if !ok {
		return nil // no capability presented; not our gate
	}
	req := limes.CheckRequest{
		Op:                op,
		Effect:            opEffects[op],
		Resource:          resourceURI,
		HasIdempotencyKey: idempotencyKeyPresent(ctx),
	}
	if err := cap.Caveats.Check(req); err != nil {
		return capabilityError(connect.CodePermissionDenied, err)
	}
	// The taint check can cost a lookup, so it runs only once everything else
	// has passed, and only where it can refuse: a read of a named object by
	// a capability that was not allowed tainted reads. Check has resolved the
	// operation's effect already, so asking again cannot fail.
	mutating, _ := req.Mutating()
	if resourceURI != "" && !mutating && !cap.Caveats.AllowTaintedRead {
		isTainted, err := tainted(ctx)
		if err != nil {
			// Fail closed: "could not tell" is not "clean".
			return capabilityError(connect.CodeUnavailable, err)
		}
		if isTainted {
			req.ResourceTainted = true
			if err := cap.Caveats.Check(req); err != nil {
				return capabilityError(connect.CodePermissionDenied, err)
			}
		}
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

func requestHasIdempotencyKey(header *connect.Header, req proto.Message) bool {
	if header.Get(paladin.HeaderIdempotencyKey) != "" {
		return true
	}
	c, ok := req.(idempotencyKeyCarrier)
	return ok && c.GetIdempotencyKey() != ""
}

type taintLookupKey struct{}

func withTaintLookup(ctx context.Context, lookup TaintLookup) context.Context {
	if lookup == nil {
		return ctx
	}
	return context.WithValue(ctx, taintLookupKey{}, lookup)
}

func taintLookupFrom(ctx context.Context) TaintLookup {
	lookup, _ := ctx.Value(taintLookupKey{}).(TaintLookup)
	return lookup
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
	return uuid.Nil()
}

// passthrough is the no-op interceptor returned when a subsystem is off.
func passthrough(next connect.ServerFunc) connect.ServerFunc { return next }
