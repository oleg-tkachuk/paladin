// Package capabilityh implements the admin CapabilityService — Issue /
// Delegate / Revoke / List for capability tokens. Wraps internal/capability
// (the in-process types + Issuer + Store) into a Connect handler suitable
// for mux registration.
//
// Authorisation:
//
//   - Issue / Revoke / List / GetUsage are gated by Cedar (IssueCapability,
//     RevokeCapability, ReadCapability). The built-in policy grants them to
//     platform.admin and platform.capability-issuer.
//   - Delegate narrows the capability the caller presents, whose own
//     caveats include OpShare and whose ID matches the requested
//     parent_id — the MCP `share`-tool path, agents minting
//     sub-capabilities for downstream callers without going through an
//     admin. Only a presented, verified capability can be delegated from.
package capabilityh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// TokenVerifier verifies a capability token, as limes.Verifier does. The
// handler verifies what it has just minted to seal it as a Biscuit:
// Issuer.Biscuit takes only a capability a verifier accepted.
type TokenVerifier interface {
	Verify(ctx context.Context, req limes.VerifyRequest) (limes.VerifiedCapability, error)
}

// Handler wires the dependencies the four RPCs need.
type Handler struct {
	issuer   *limes.Issuer
	verifier TokenVerifier
	store    limes.Store
	usage    meter.UsageStore
	policy   cedar.Authorizer

	// copier and copies serve RevokeBiscuit; see WithBiscuitCopies.
	copier BiscuitCopier
	copies limes.BiscuitRevocationStore
	// copyUsage serves GetBiscuitUsage; see WithCopyUsage.
	copyUsage meter.CopyUsageReader
	// records serves List and Get; see WithRecords.
	records limes.RecordReporter

	// currency is the platform's (capability.currency): what a root
	// capability's budget is in when it names none. See WithCurrency.
	currency string
}

// WithCurrency sets the platform's currency, capability.currency. Without it
// a budget that names none is in platformUnit, as limes reads one.
func (h *Handler) WithCurrency(unit string) *Handler {
	h.currency = unit
	return h
}

// NewHandler builds the Handler. issuer / verifier / store / policy are
// required; passing nil panics — wiring bugs should fail loud at boot. usage
// may be nil when the operator hasn't wired the runtime-counter store —
// GetUsage then returns CodeUnavailable so the misconfig is visible.
func NewHandler(
	issuer *limes.Issuer,
	verifier TokenVerifier,
	store limes.Store,
	usage meter.UsageStore,
	policy cedar.Authorizer,
) *Handler {
	if issuer == nil || verifier == nil || store == nil || policy == nil {
		panic("capabilityh: issuer / verifier / store / policy are required")
	}
	return &Handler{issuer: issuer, verifier: verifier, store: store, usage: usage, policy: policy, currency: apiutil.NoUnit}
}

// hasOp reports whether the caveat op-set includes the requested op.
// Empty op-set means "no operation allowed", so the result is false in
// that case.
func hasOp(ops []limes.Op, want limes.Op) bool {
	for _, o := range ops {
		if o == want {
			return true
		}
	}
	return false
}

// authorize gates an RPC against Cedar, with the caller's own tenant as the
// resource. platform.admin and platform.capability-issuer are admitted by the
// built-in policy (cedar.builtinPolicy); a tenant policy may grant the actions
// to its own members. Reaching another tenant is the handler's call
// (spansTenants), never Cedar's.
func (h *Handler) authorize(ctx context.Context, action cedar.Action) (*auth.Principal, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{TenantID: p.TenantID, TenantSlug: p.TenantSlug},
		cedar.RequestContext{},
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if decision != cedar.DecisionAllow {
		return nil, connect.Errorf(connect.CodePermissionDenied, "%s denied", action)
	}
	return p, nil
}

// spansTenants reports whether the caller may manage capabilities of tenants
// other than its own: platform.admin, or platform.capability-issuer — the
// narrow grant for a consumer that mints a capability per tenant it serves.
func spansTenants(caller *auth.Principal) bool {
	return caller.HasRole(apiutil.RolePlatformAdmin) || caller.HasRole(apiutil.RoleCapabilityIssuer)
}

// actOnCapabilitysTenant scopes ctx to the tenant that owns capability id, for
// a caller that spans tenants. Revoke and GetUsage name a capability by id
// alone, and its rows are RLS-isolated by its own tenant, so on the caller's
// connection a platform admin saw nothing and every call answered NotFound.
//
// The owner is read under the cross-tenant flag, which widens that one read
// and lets nothing write. A caller confined to its own tenant gets ctx back
// unchanged: RLS keeps scoping it, and another tenant's id stays NotFound
// rather than becoming an oracle. An id nobody owns is left to the store call,
// which answers NotFound the same way.
func (h *Handler) actOnCapabilitysTenant(ctx context.Context, caller *auth.Principal, id uuid.UUID) (context.Context, error) {
	if !spansTenants(caller) {
		return ctx, nil
	}
	rec, err := h.store.GetRecord(auth.WithCrossTenantRead(ctx), id)
	switch {
	case errors.Is(err, limes.ErrNotFound):
		return ctx, nil
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return auth.WithActingTenant(ctx, rec.Capability.Subject.TenantID), nil
}

// Issue mints a top-level capability. Gated by IssueCapability; issuing for
// another tenant additionally requires a role that spans tenants.
func (h *Handler) Issue(ctx context.Context, req *adminv1.CapabilityServiceIssueRequest) (*adminv1.CapabilityServiceIssueResponse, error) {
	caller, err := h.authorize(ctx, cedar.ActionIssueCapability)
	if err != nil {
		return nil, err
	}

	subj, err := protoToPrincipal(req.GetSubject())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	// Issuing FOR another tenant is a platform operation.
	//
	// The subject's tenant is chosen by the caller and Cedar authorises
	// IssueCapability against the CALLER's own tenant policy, which says
	// nothing about the tenant named in the subject. So without this check any
	// tenant granted IssueCapability in its own policy could mint a capability for any
	// other tenant — and since ADR-0010 a capability authenticates as its
	// subject's tenant, that is a full cross-tenant escalation, not merely an
	// extra restriction on an existing caller.
	if subj.TenantID != caller.TenantID && !spansTenants(caller) {
		// platform.capability-issuer is the narrow grant for exactly this: a
		// consumer serving many tenants mints a short-lived capability per
		// tenant. It carries no other authority — it cannot create or delete a
		// tenant, and it cannot mint an API token — so handing it out is a much
		// smaller decision than handing out platform.admin.
		return nil, connect.NewError(connect.CodePermissionDenied, "issuing a capability for another tenant requires platform.admin or platform.capability-issuer")
	}

	// A top-level capability has no parent whose ceilings it could keep.
	if err := requireCeilings(req.GetCaveats()); err != nil {
		return nil, err
	}
	caveats, err := protoToCaveats(req.GetCaveats(), nil, h.currency)
	if err != nil {
		return nil, err
	}

	ttl := time.Duration(req.GetTtlSeconds()) * time.Second
	var nbf time.Time
	if t := req.GetNotBefore(); t.IsValid() {
		nbf = t.AsTime()
	}

	if req.GetRequireBiscuit() {
		if err := requireBiscuit(req.GetConfirmationJkt(), nbf); err != nil {
			return nil, err
		}
	}

	// The row lands under the subject's tenant, so the connection has to be
	// scoped there — the cross-tenant gate above is what makes this safe.
	ctx = auth.WithActingTenant(ctx, subj.TenantID)
	res, err := h.issuer.Issue(ctx, limes.IssueRequest{
		Subject: subj,
		// Who ASKED for this capability, as opposed to who it authorises.
		// Taken from the authorized caller so it cannot be spoofed by the
		// request body.
		IssuedBy: limes.Principal{
			TenantID: caller.TenantID,
			Subject:  caller.Subject,
			Type:     limes.PrincipalTypeUser,
		},
		Audience:        req.GetAudience(),
		Caveats:         caveats,
		TTL:             ttl,
		NotBefore:       nbf,
		ConfirmationJKT: req.GetConfirmationJkt(),
	})
	if err != nil {
		// By kind: a malformed request, a tenant that is not (or no longer)
		// there, or the store or signer failing — which is not the caller's
		// fault and must not read as a request to stop retrying.
		return nil, apiutil.MapError(err)
	}

	apiutil.StashResource(ctx, capabilityResourceName(res.Capability.Subject.TenantID, res.Capability.ID))
	return h.issued(ctx, res)
}

// Delegate narrows the capability the caller presents. Only a presented
// capability can be delegated from: Issuer.Delegate takes the parent as the
// verifier accepted it, never as a record reads, so the token that grants
// what is narrowed is the one checked — signature, expiry, revocation and
// the attenuation of a Biscuit copy. The caller presents it via the
// X-Paladin-Capability header, alone or beside a JWT. Its caveats MUST
// include `OpShare`, AND `parent_id` MUST equal its own ID, so an agent
// cannot delegate off some other principal's capability.
//
// Caveat narrowing is enforced downstream by Issuer.Delegate
// (ErrDelegationTooWide); this handler only gates *who* may delegate.
func (h *Handler) Delegate(ctx context.Context, req *adminv1.CapabilityServiceDelegateRequest) (*adminv1.CapabilityServiceIssueResponse, error) {
	parentID, err := uuid.Parse(req.GetParentId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("parent_id: %w", err))
	}

	// Gated entirely by the presented capability's own caveats — no Cedar
	// admin check: the capability is the grant.
	verified, ok := auth.VerifiedCapabilityFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			"capability: delegation narrows a presented capability; send it in the "+auth.HeaderCapability+" header")
	}
	parent := verified.Capability()
	if !hasOp(parent.Caveats.Ops, limes.OpShare) {
		return nil, connect.NewError(connect.CodePermissionDenied, "capability: caller lacks OpShare")
	}
	if parent.ID != parentID {
		return nil, connect.NewError(connect.CodePermissionDenied, "capability: parent_id must equal caller's own capability id")
	}
	// A Biscuit copy's attenuation, and any limits of its own, are not in a
	// token the issuer can extend: a child would shed them. Its holder
	// narrows offline instead. Issuer.Delegate refuses it too, as
	// ErrDelegationTooWide; asking first answers with a precondition the
	// caller can act on, not a permission denial.
	if verified.FromBiscuit() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "capability: a Biscuit copy cannot delegate; attenuate it instead")
	}

	// No caveats in the request means "the parent's, unchanged" — asked for
	// explicitly, so that caveats which ARE sent are never silently replaced.
	var caveats limes.Caveats
	inherit := req.GetCaveats() == nil
	if !inherit {
		// A ceiling left unset keeps the parent's, in the parent's unit.
		if caveats, err = protoToCaveats(req.GetCaveats(), &parent.Caveats, h.currency); err != nil {
			return nil, err
		}
	}
	audience := parent.Audience
	if a := req.GetAudience(); len(a) > 0 {
		audience = a
	}
	ttl := time.Duration(req.GetTtlSeconds()) * time.Second
	var nbf time.Time
	if t := req.GetNotBefore(); t.IsValid() {
		nbf = t.AsTime()
	}

	// Resolve subject explicitly: msg.subject if supplied, else parent's.
	delegSubj := parent.Subject
	if msg := req.GetSubject(); msg != nil {
		s, perr := protoToPrincipal(msg)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, perr.Error()).WithCause(perr)
		}
		delegSubj = s
	}

	if req.GetRequireBiscuit() {
		// The child as limes will mint it: bound to the parent's key unless
		// it names its own.
		jkt := req.GetConfirmationJkt()
		if jkt == "" {
			jkt = parent.ConfirmationJKT
		}
		if err := requireBiscuit(jkt, nbf); err != nil {
			return nil, err
		}
	}

	// The child is recorded in the parent's tenant, where the parent's own
	// record is: the request's principal may be bound to another (an admin's
	// to the platform tenant), and the store is scoped by the tenant the
	// request acts on. The presented capability is the grant for acting there.
	ctx = auth.WithActingTenant(ctx, parent.Subject.TenantID)
	res, err := h.issuer.Delegate(ctx, limes.DelegateRequest{
		Parent:          verified,
		Subject:         delegSubj,
		Audience:        audience,
		Caveats:         caveats,
		InheritCaveats:  inherit,
		TTL:             ttl,
		NotBefore:       nbf,
		ConfirmationJKT: req.GetConfirmationJkt(),
	})
	if err != nil {
		switch {
		case errors.Is(err, limes.ErrDelegationTooWide),
			errors.Is(err, limes.ErrUnitCodeMismatch),
			errors.Is(err, limes.ErrOpNotAllowed):
			return nil, connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
		case errors.Is(err, limes.ErrRevoked), errors.Is(err, limes.ErrExpired):
			// The parent can no longer delegate; that is a state the caller
			// must act on, not a malformed request.
			return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
		}
		return nil, apiutil.MapError(err)
	}
	apiutil.StashResource(ctx, capabilityResourceName(res.Capability.Subject.TenantID, res.Capability.ID))
	return h.issued(ctx, res)
}

// Revoke adds the capability ID to the revocation list. Idempotent for a
// capability the caller can see; NotFound for one it cannot.
func (h *Handler) Revoke(ctx context.Context, req *adminv1.CapabilityServiceRevokeRequest) (*adminv1.CapabilityServiceRevokeResponse, error) {
	caller, err := h.authorize(ctx, cedar.ActionRevokeCapability)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("id: %w", err))
	}
	ctx, err = h.actOnCapabilitysTenant(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	if _, err := h.store.Revoke(ctx, limes.RevokeRequest{
		CapabilityID:    id,
		Note:            req.GetReason(),
		Actor:           caller.Subject,
		CascadeChildren: req.GetCascadeChildren(),
	}); err != nil {
		// The store scopes the revoke to capabilities the caller can see, so
		// a not-found here covers both "no such id" and "belongs to another
		// tenant". Both answer NotFound: telling the caller which one it was
		// would turn this endpoint into an id oracle.
		if errors.Is(err, limes.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	stashCapabilityInScope(ctx, id)
	return &adminv1.CapabilityServiceRevokeResponse{}, nil
}

// Get returns one capability as it is on record. Gated as List and GetUsage
// are, and scoped to the capability's tenant the way GetUsage is.
func (h *Handler) Get(ctx context.Context, req *adminv1.CapabilityServiceGetRequest) (*adminv1.CapabilityServiceGetResponse, error) {
	caller, err := h.authorize(ctx, cedar.ActionReadCapability)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("id: %w", err))
	}
	ctx, err = h.actOnCapabilitysTenant(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	if h.records == nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "capability record listing not wired")
	}
	// The record with its disposition by the store's rules — revoked through
	// an ancestor too, though the record holds only its own entry.
	listed, err := h.records.GetRecordListing(ctx, id)
	if err != nil {
		if errors.Is(err, limes.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
		}
		return nil, apiutil.MapError(err)
	}
	rec := listed.Record
	out := &adminv1.CapabilityServiceGetResponse{
		Capability: capabilityToProto(&rec.Capability),
		IssuedBy:   principalToProto(rec.IssuedBy),
	}
	out.Capability.Disposition = dispositionToProto(listed.Disposition)
	if r := rec.Revocation; r != nil {
		out.Revocation = &adminv1.CapabilityRevocation{
			RevokedAt: timestamppb.New(r.RevokedAt), Reason: r.Note, Actor: r.Actor, Cascade: r.Cascaded,
		}
	}
	return out, nil
}

// GetUsage returns the runtime counters (request_count, spent_usd)
// for a capability. NOT_FOUND when the cap has never been used —
// distinct from "exists but unused" (the caller's List having
// returned the cap proves it exists; an absent usage row just
// means no Charge / no BumpRequest has fired yet).
//
// Authorization: same Cedar action as List (read-side admin), so
// the operator who can List a tenant's caps can also see their
// usage.
func (h *Handler) GetUsage(ctx context.Context, req *adminv1.CapabilityServiceGetUsageRequest) (*adminv1.CapabilityServiceGetUsageResponse, error) {
	caller, err := h.authorize(ctx, cedar.ActionReadCapability)
	if err != nil {
		return nil, err
	}
	if h.usage == nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "capability runtime counter store not wired")
	}
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("id: %w", err))
	}
	ctx, err = h.actOnCapabilitysTenant(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	u, err := h.usage.GetUsage(ctx, id)
	if err != nil {
		if errors.Is(err, meter.ErrUsageNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &adminv1.CapabilityServiceGetUsageResponse{
		CapabilityId: u.CapabilityID.String(),
		RequestCount: u.RequestCount,
		Spent:        apiutil.MoneyOf(u.UnitCode, u.SpentAmount),
		// updated_at not surfaced today — the UsageStore.Get value
		// doesn't carry it consistently across the postgres /
		// metering decorators. Add when telemetry needs it.
	}, nil
}

// ─── proto ↔ domain converters ──────────────────────────────────────────────

func protoToPrincipal(p *adminv1.CapabilityPrincipal) (limes.Principal, error) {
	if p == nil {
		return limes.Principal{}, errors.New("principal required")
	}
	tenantID, err := uuid.Parse(p.GetTenantId())
	if err != nil {
		return limes.Principal{}, fmt.Errorf("subject.tenant_id: %w", err)
	}
	out := limes.Principal{
		Type:     protoToPrincipalKind(p.GetKind()),
		TenantID: tenantID,
		Subject:  p.GetSubject(),
	}
	if p.GetKind() == adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT {
		out.Agent = &limes.AgentPrincipal{
			AgentType:    p.GetAgentType(),
			AgentVersion: p.GetAgentVersion(),
			Model:        p.GetModel(),
			MCPClient:    p.GetMcpClient(),
		}
		if rid := p.GetRunId(); rid != "" {
			parsed, err := uuid.Parse(rid)
			if err != nil {
				return limes.Principal{}, fmt.Errorf("subject.run_id: %w", err)
			}
			out.Agent.RunID = parsed
		}
		if pid := p.GetParentRunId(); pid != "" {
			parsed, err := uuid.Parse(pid)
			if err != nil {
				return limes.Principal{}, fmt.Errorf("subject.parent_run_id: %w", err)
			}
			out.Agent.ParentRunID = parsed
		}
	}
	return out, nil
}

func protoToPrincipalKind(k adminv1.PrincipalKind) limes.PrincipalType {
	switch k {
	case adminv1.PrincipalKind_PRINCIPAL_KIND_USER:
		return limes.PrincipalTypeUser
	case adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT:
		return limes.PrincipalTypeAgent
	case adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE:
		return limes.PrincipalTypeService
	default:
		return ""
	}
}

// capabilitiesSegment names a tenant's capabilities in a resource name.
const capabilitiesSegment = "/capabilities/"

// capabilityResourceName is the name the audit log files a capability under,
// `tenants/{tenant}/capabilities/{id}`: the API addresses a capability by id
// alone, which names no tenant, and the tenant is what puts the row in that
// tenant's trail.
func capabilityResourceName(tenantID, id uuid.UUID) string {
	return apiutil.TenantNamePrefix + tenantID.String() + capabilitiesSegment + id.String()
}

// stashCapabilityInScope names, for the audit log, a capability the request
// acted on by id. Its tenant is the one actOnCapabilitysTenant switched to,
// or the caller's own — the only one a caller confined to it can act in.
func stashCapabilityInScope(ctx context.Context, id uuid.UUID) {
	if tenantID, err := auth.EffectiveTenant(ctx); err == nil {
		apiutil.StashResource(ctx, capabilityResourceName(tenantID, id))
	}
}

// issued answers Issue and Delegate: the capability, its JWT, and the same
// capability as a Biscuit its holder can narrow offline — or why not.
func (h *Handler) issued(ctx context.Context, res limes.IssueResult) (*adminv1.CapabilityServiceIssueResponse, error) {
	bisc, status, err := h.seal(ctx, res)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &adminv1.CapabilityServiceIssueResponse{
		Capability:    capabilityToProto(&res.Capability),
		Token:         res.Token,
		Biscuit:       bisc,
		BiscuitStatus: status,
	}, nil
}

// biscuitStatusOf is whether a capability bound to the key jkt names and
// valid from nbf can be sealed as a Biscuit at now, and why not. A key-bound
// one verifies only with a proof of its holder's key, which the server does
// not have, and one valid only from a later not-before verifies only then:
// their holders present the JWT. Share is not needed: a copy narrowed from
// the Biscuit allows no more than the JWT, which its bearer could hand on as
// it is, and a copy with limits of its own is counted against the
// capability's copy quota.
func biscuitStatusOf(jkt string, nbf, now time.Time) adminv1.BiscuitStatus {
	switch {
	case jkt != "":
		return adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND
	case nbf.After(now):
		return adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID
	default:
		return adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED
	}
}

// biscuitRefusals says, for require_biscuit, why each status leaves a
// capability without its Biscuit.
var biscuitRefusals = map[adminv1.BiscuitStatus]string{
	adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND:     "it is bound to a key, and only its holder can prove it; present the token",
	adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID: "it is valid only from a later not_before",
}

// requireBiscuit refuses, before anything is issued, a capability that could
// not be sealed as a Biscuit, as FailedPrecondition naming why.
func requireBiscuit(jkt string, nbf time.Time) error {
	status := biscuitStatusOf(jkt, nbf, time.Now())
	if status == adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED {
		return nil
	}
	return connect.Errorf(connect.CodeFailedPrecondition,
		"require_biscuit: the capability cannot be sealed as a Biscuit (%s): %s", status, biscuitRefusals[status])
}

// seal is the capability just minted as a Biscuit its holder can narrow
// offline, with its status, or no Biscuit and the status that says why.
// Issuer.Biscuit seals only a capability a verifier accepted, so the token is
// verified first, for an audience it names.
func (h *Handler) seal(ctx context.Context, res limes.IssueResult) (string, adminv1.BiscuitStatus, error) {
	c := res.Capability
	status := biscuitStatusOf(c.ConfirmationJKT, c.NotBefore, time.Now())
	if status != adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED {
		return "", status, nil
	}
	verified, err := h.verifier.Verify(ctx, limes.VerifyRequest{Token: res.Token, Audience: c.Audience[0]})
	if errors.Is(err, limes.ErrNotYetValid) {
		// A not-before the clock reached between the check and the verify.
		return "", adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("capabilityh: verify the minted capability: %w", err)
	}
	b, err := h.issuer.Biscuit(ctx, limes.BiscuitRequest{Capability: verified})
	if err != nil {
		return "", 0, err
	}
	return b.Token, status, nil
}

// protoToCaveats reads c. A ceiling c leaves unset is parent's, in parent's
// unit; with no parent the caller has refused that already (requireCeilings).
// A budget that names no currency is in parent's unit — a delegate counts in
// what it draws from — or, at the root, in currency, the platform's.
func protoToCaveats(c *adminv1.CapabilityCaveats, parent *limes.Caveats, currency string) (limes.Caveats, error) {
	if c == nil {
		return limes.Caveats{}, nil
	}
	if err := apiutil.RefuseRemovedFields(c); err != nil {
		return limes.Caveats{}, err
	}
	var inherited limes.Caveats
	if parent != nil {
		inherited = *parent
	}
	requests, ok := requestCeilingOf(c)
	if !ok {
		requests = inherited.Requests
	}
	budget, ok, err := budgetCeilingOf(c)
	if err != nil {
		return limes.Caveats{}, err
	}
	if !ok {
		budget = budgetCeiling{ceiling: inherited.Budget, unit: inherited.Budget.UnitCode}
	}
	if budget.unit == "" {
		budget.unit = currency
		if parent != nil && parent.Budget.UnitCode != "" {
			budget.unit = parent.Budget.UnitCode
		}
	}
	budget.ceiling.UnitCode = budget.unit
	copies, ok := copyQuotaOf(c)
	if !ok {
		copies = limes.CopyQuota{Unlimited: true}
		if parent != nil {
			copies = parent.Copies
		}
	}
	out := limes.Caveats{
		ResourcePrefixes:       c.GetResourcePrefixes(),
		ResourceURIs:           c.GetResourceUris(),
		Requests:               requests,
		Budget:                 budget.ceiling,
		Copies:                 copies,
		AllowTaintedRead:       c.GetAllowTaintedRead(),
		IdempotencyKeyRequired: c.GetIdempotencyKeyRequired(),
		SourceIPCIDR:           c.GetSourceIpCidr(),
	}
	for _, op := range c.GetOps() {
		out.Ops = append(out.Ops, limes.Op(op))
	}
	return out, nil
}

func principalToProto(p limes.Principal) *adminv1.CapabilityPrincipal {
	out := &adminv1.CapabilityPrincipal{
		Kind:     principalKindToProto(p.Type),
		TenantId: p.TenantID.String(),
		Subject:  p.Subject,
	}
	if p.Agent != nil {
		out.AgentType = p.Agent.AgentType
		out.AgentVersion = p.Agent.AgentVersion
		out.Model = p.Agent.Model
		out.McpClient = p.Agent.MCPClient
		if p.Agent.RunID != uuid.Nil {
			out.RunId = p.Agent.RunID.String()
		}
		if p.Agent.ParentRunID != uuid.Nil {
			out.ParentRunId = p.Agent.ParentRunID.String()
		}
	}
	return out
}

func principalKindToProto(t limes.PrincipalType) adminv1.PrincipalKind {
	switch t {
	case limes.PrincipalTypeUser:
		return adminv1.PrincipalKind_PRINCIPAL_KIND_USER
	case limes.PrincipalTypeAgent:
		return adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT
	case limes.PrincipalTypeService:
		return adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE
	default:
		return adminv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
	}
}

func caveatsToProto(c limes.Caveats) *adminv1.CapabilityCaveats {
	out := &adminv1.CapabilityCaveats{
		ResourcePrefixes:       c.ResourcePrefixes,
		ResourceUris:           c.ResourceURIs,
		AllowTaintedRead:       c.AllowTaintedRead,
		IdempotencyKeyRequired: c.IdempotencyKeyRequired,
		SourceIpCidr:           c.SourceIPCIDR,
	}
	setRequestCeiling(out, c.Requests)
	setBudgetCeiling(out, c.Budget, c.Budget.UnitCode)
	setCopyQuota(out, c.Copies)
	for _, op := range c.Ops {
		out.Ops = append(out.Ops, string(op))
	}
	return out
}

func capabilityToProto(c *limes.Capability) *adminv1.Capability {
	out := &adminv1.Capability{
		Id:              c.ID.String(),
		Issuer:          c.Issuer,
		Subject:         principalToProto(c.Subject),
		Audience:        c.Audience,
		Caveats:         caveatsToProto(c.Caveats),
		IssuedAt:        timestamppb.New(c.IssuedAt),
		ExpiresAt:       timestamppb.New(c.ExpiresAt),
		ConfirmationJkt: c.ConfirmationJKT,
	}
	if !c.NotBefore.IsZero() {
		out.NotBefore = timestamppb.New(c.NotBefore)
	}
	if c.ParentID != uuid.Nil {
		out.ParentId = c.ParentID.String()
	}
	return out
}
