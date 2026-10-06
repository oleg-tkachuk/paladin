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
//   - Delegate accepts EITHER a Cedar DelegateCapability grant OR a capability-
//     authenticated caller whose own capability includes OpShare AND
//     whose ID matches the requested parent_id. The latter is the
//     MCP `share`-tool path — agents minting sub-capabilities for
//     downstream callers without going through an admin.
package capabilityh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/safecast"

	"github.com/jackc/pgx/v5"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// Handler wires the dependencies the four RPCs need.
type Handler struct {
	issuer *capability.Issuer
	store  capability.Store
	usage  capability.UsageStore[pgx.Tx]
	policy cedar.Authorizer

	// copier and copies serve RevokeBiscuit; see WithBiscuitCopies.
	copier BiscuitCopier
	copies capability.BiscuitRevocationStore
	// copyUsage serves GetBiscuitUsage; see WithCopyUsage.
	copyUsage capability.CopyUsageReader
}

// NewHandler builds the Handler. issuer / store / policy are required;
// passing nil panics — wiring bugs should fail loud at boot. usage may
// be nil when the operator hasn't wired the runtime-counter store —
// GetUsage then returns CodeUnavailable so the misconfig is visible.
func NewHandler(
	issuer *capability.Issuer,
	store capability.Store,
	usage capability.UsageStore[pgx.Tx],
	policy cedar.Authorizer,
) *Handler {
	if issuer == nil || store == nil || policy == nil {
		panic("capabilityh: issuer / store / policy are required")
	}
	return &Handler{issuer: issuer, store: store, usage: usage, policy: policy}
}

// hasOp reports whether the caveat op-set includes the requested op.
// Empty op-set means "no operation allowed", so the result is false in
// that case.
func hasOp(ops []capability.Op, want capability.Op) bool {
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
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{TenantID: p.TenantID, TenantSlug: p.TenantSlug},
		cedar.RequestContext{},
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if decision != cedar.DecisionAllow {
		return nil, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("%s denied", action))
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
	c, err := h.store.Get(auth.WithCrossTenantRead(ctx), id)
	switch {
	case errors.Is(err, capability.ErrNotFound):
		return ctx, nil
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return auth.WithActingTenant(ctx, c.Subject.TenantID), nil
}

// Issue mints a top-level capability. Gated by IssueCapability; issuing for
// another tenant additionally requires a role that spans tenants.
func (h *Handler) Issue(ctx context.Context, req *connect.Request[adminv1.CapabilityServiceIssueRequest]) (*connect.Response[adminv1.CapabilityServiceIssueResponse], error) {
	caller, err := h.authorize(ctx, cedar.ActionIssueCapability)
	if err != nil {
		return nil, err
	}

	subj, err := protoToPrincipal(req.Msg.GetSubject())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
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
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("issuing a capability for another tenant requires platform.admin or platform.capability-issuer"))
	}

	caveats, err := protoToCaveats(req.Msg.GetCaveats())
	if err != nil {
		return nil, err
	}

	ttl := time.Duration(req.Msg.GetTtlSeconds()) * time.Second
	var nbf time.Time
	if t := req.Msg.GetNotBefore(); t.IsValid() {
		nbf = t.AsTime()
	}

	// The row lands under the subject's tenant, so the connection has to be
	// scoped there — the cross-tenant gate above is what makes this safe.
	ctx = auth.WithActingTenant(ctx, subj.TenantID)
	cap, token, err := h.issuer.Issue(ctx, capability.IssueRequest{
		Subject: subj,
		// Who ASKED for this capability, as opposed to who it authorises.
		// Taken from the authorized caller so it cannot be spoofed by the
		// request body.
		IssuedBy: capability.Principal{
			TenantID: caller.TenantID,
			Subject:  caller.Subject,
			Type:     capability.PrincipalUser,
		},
		Audience:        req.Msg.GetAudience(),
		Caveats:         caveats,
		TTL:             ttl,
		NotBefore:       nbf,
		ConfirmationJKT: req.Msg.GetConfirmationJkt(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	apiutil.StashResource(ctx, capabilityResourceName(cap.Subject.TenantID, cap.ID))
	return h.issued(cap, token)
}

// Delegate narrows a parent capability. Two authentication paths are
// accepted:
//
//  1. Admin (JWT or role-bearing API token) — Cedar action
//     DelegateCapability gates the call. The parent is read under the
//     caller's own tenant, so only that tenant's capabilities are reachable.
//
//  2. Capability-bearing caller (the agent share-tool path) — caller
//     must present a verified capability via the X-Paladin-Capability
//     header. The caller's capability MUST include `OpShare` in its
//     allowed ops, AND `parent_id` MUST equal the caller's own
//     capability ID. This prevents an agent from delegating off some
//     other principal's capability.
//
// Caveat narrowing is enforced downstream by Issuer.Delegate
// (ErrDelegationTooWide); this handler only gates *who* may delegate.
func (h *Handler) Delegate(ctx context.Context, req *connect.Request[adminv1.CapabilityServiceDelegateRequest]) (*connect.Response[adminv1.CapabilityServiceIssueResponse], error) {
	parentID, err := uuid.Parse(req.Msg.GetParentId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("parent_id: %w", err))
	}

	var parent *capability.Capability
	// Path 2: capability-authenticated caller. Gated entirely by the
	// caller's own caveats — no Cedar admin check.
	if callerCap, ok := auth.CapabilityFromContext(ctx); ok {
		if !hasOp(callerCap.Caveats.Ops, capability.OpShare) {
			return nil, connect.NewError(connect.CodePermissionDenied,
				errors.New("capability: caller lacks OpShare"))
		}
		if callerCap.ID != parentID {
			return nil, connect.NewError(connect.CodePermissionDenied,
				errors.New("capability: parent_id must equal caller's own capability id"))
		}
		// A child's requests and spend reach its ancestors' counters, not a
		// Biscuit copy's own, so a copy with limits of its own would shed them
		// by delegating. Its holder narrows offline instead.
		if len(callerCap.Copies) > 0 {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("capability: a Biscuit copy with limits of its own cannot delegate; attenuate it instead"))
		}
		// Narrow from the capability as presented: a Biscuit copy's
		// attenuation is not in the stored record, and narrowing from the
		// record would hand the child what the copy gave up.
		parent = callerCap
	} else {
		// Path 1: admin. Re-use the existing Cedar gate.
		if _, err := h.authorize(ctx, cedar.ActionDelegateCapability); err != nil {
			return nil, err
		}
		if parent, err = h.store.Get(ctx, parentID); err != nil {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
	}

	// No caveats in the request means "the parent's, unchanged" — asked for
	// explicitly, so that caveats which ARE sent are never silently replaced.
	var caveats capability.Caveats
	inherit := req.Msg.GetCaveats() == nil
	if !inherit {
		if caveats, err = protoToCaveats(req.Msg.GetCaveats()); err != nil {
			return nil, err
		}
	}
	audience := parent.Audience
	if a := req.Msg.GetAudience(); len(a) > 0 {
		audience = a
	}
	ttl := time.Duration(req.Msg.GetTtlSeconds()) * time.Second
	var nbf time.Time
	if t := req.Msg.GetNotBefore(); t.IsValid() {
		nbf = t.AsTime()
	}

	// Resolve subject explicitly: msg.subject if supplied, else parent's.
	delegSubj := parent.Subject
	if msg := req.Msg.GetSubject(); msg != nil {
		s, perr := protoToPrincipal(msg)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, perr)
		}
		delegSubj = s
	}

	cap, token, err := h.issuer.Delegate(ctx, capability.DelegateRequest{
		Parent:          *parent,
		Subject:         delegSubj,
		Audience:        audience,
		Caveats:         caveats,
		InheritCaveats:  inherit,
		TTL:             ttl,
		NotBefore:       nbf,
		ConfirmationJKT: req.Msg.GetConfirmationJkt(),
	})
	if err != nil {
		switch {
		case errors.Is(err, capability.ErrDelegationTooWide),
			errors.Is(err, capability.ErrUnitCodeMismatch):
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		case errors.Is(err, capability.ErrRevoked), errors.Is(err, capability.ErrExpired):
			// The parent can no longer delegate; that is a state the caller
			// must act on, not a malformed request.
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	apiutil.StashResource(ctx, capabilityResourceName(cap.Subject.TenantID, cap.ID))
	return h.issued(cap, token)
}

// Revoke adds the capability ID to the revocation list. Idempotent for a
// capability the caller can see; NotFound for one it cannot.
func (h *Handler) Revoke(ctx context.Context, req *connect.Request[adminv1.CapabilityServiceRevokeRequest]) (*connect.Response[adminv1.CapabilityServiceRevokeResponse], error) {
	caller, err := h.authorize(ctx, cedar.ActionRevokeCapability)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("id: %w", err))
	}
	ctx, err = h.actOnCapabilitysTenant(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	if err := h.store.Revoke(ctx, capability.RevokeArgs{
		ID:              id,
		Reason:          req.Msg.GetReason(),
		Actor:           caller.Subject,
		CascadeChildren: req.Msg.GetCascadeChildren(),
	}); err != nil {
		// The store scopes the revoke to capabilities the caller can see, so
		// a not-found here covers both "no such id" and "belongs to another
		// tenant". Both answer NotFound: telling the caller which one it was
		// would turn this endpoint into an id oracle.
		if errors.Is(err, capability.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	stashCapabilityInScope(ctx, id)
	return connect.NewResponse(&adminv1.CapabilityServiceRevokeResponse{}), nil
}

// List enumerates capabilities issued to a principal.
func (h *Handler) List(ctx context.Context, req *connect.Request[adminv1.CapabilityServiceListRequest]) (*connect.Response[adminv1.CapabilityServiceListResponse], error) {
	caller, err := h.authorize(ctx, cedar.ActionReadCapability)
	if err != nil {
		return nil, err
	}

	tenantID, err := uuid.Parse(req.Msg.GetTenantId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	// Same gate as Issue: ReadCapability in a tenant's own policy must not become a
	// read of every other tenant's capabilities. Cedar above authorised the
	// ACTION against the caller's own tenant, not against this one.
	if tenantID != caller.TenantID && !spansTenants(caller) {
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("listing another tenant's capabilities requires platform.admin or platform.capability-issuer"))
	}
	// capability_records is RLS-isolated, so reading another tenant's rows
	// needs the connection scoped to that tenant. Without this the admin
	// console renders an empty list instead of an error — RLS filters.
	ctx = auth.WithActingTenant(ctx, tenantID)
	caps, next, err := h.store.ListByPrincipal(ctx, capability.ListByPrincipalArgs{
		TenantID:       tenantID,
		PrincipalT:     protoToPrincipalKind(req.Msg.GetPrincipalKind()),
		Subject:        req.Msg.GetSubject(),
		IncludeExpired: req.Msg.GetIncludeExpired(),
		IncludeRevoked: req.Msg.GetIncludeRevoked(),
		Cursor:         req.Msg.GetPageToken(),
		Limit:          req.Msg.GetPageSize(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	out := make([]*adminv1.Capability, 0, len(caps))
	for i := range caps {
		out = append(out, capabilityToProto(&caps[i]))
	}
	return connect.NewResponse(&adminv1.CapabilityServiceListResponse{
		Capabilities:  out,
		NextPageToken: next,
	}), nil
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
func (h *Handler) GetUsage(ctx context.Context, req *connect.Request[adminv1.CapabilityServiceGetUsageRequest]) (*connect.Response[adminv1.CapabilityServiceGetUsageResponse], error) {
	caller, err := h.authorize(ctx, cedar.ActionReadCapability)
	if err != nil {
		return nil, err
	}
	if h.usage == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("capability runtime counter store not wired"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("id: %w", err))
	}
	ctx, err = h.actOnCapabilitysTenant(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	u, err := h.usage.Get(ctx, id)
	if err != nil {
		if errors.Is(err, capability.ErrUsageNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	unit := u.UnitCode
	if unit == "" {
		unit = capability.DefaultUnitCode
	}
	return connect.NewResponse(&adminv1.CapabilityServiceGetUsageResponse{
		CapabilityId: u.CapabilityID.String(),
		RequestCount: u.RequestCount,
		SpentMicros:  apiutil.Micros(u.SpentAmount),
		UnitCode:     unit,
		// updated_at not surfaced today — the UsageStore.Get value
		// doesn't carry it consistently across the postgres /
		// metering decorators. Add when telemetry needs it.
	}), nil
}

// ─── proto ↔ domain converters ──────────────────────────────────────────────

func protoToPrincipal(p *adminv1.CapabilityPrincipal) (capability.Principal, error) {
	if p == nil {
		return capability.Principal{}, errors.New("principal required")
	}
	tenantID, err := uuid.Parse(p.GetTenantId())
	if err != nil {
		return capability.Principal{}, fmt.Errorf("subject.tenant_id: %w", err)
	}
	out := capability.Principal{
		Type:     protoToPrincipalKind(p.GetKind()),
		TenantID: tenantID,
		Subject:  p.GetSubject(),
	}
	if p.GetKind() == adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT {
		out.Agent = &capability.AgentPrincipal{
			AgentType:    p.GetAgentType(),
			AgentVersion: p.GetAgentVersion(),
			Model:        p.GetModel(),
			MCPClient:    p.GetMcpClient(),
		}
		if rid := p.GetRunId(); rid != "" {
			parsed, err := uuid.Parse(rid)
			if err != nil {
				return capability.Principal{}, fmt.Errorf("subject.run_id: %w", err)
			}
			out.Agent.RunID = parsed
		}
		if pid := p.GetParentAgentId(); pid != "" {
			parsed, err := uuid.Parse(pid)
			if err != nil {
				return capability.Principal{}, fmt.Errorf("subject.parent_agent_id: %w", err)
			}
			out.Agent.ParentAgentID = parsed
		}
	}
	return out, nil
}

func protoToPrincipalKind(k adminv1.PrincipalKind) capability.PrincipalType {
	switch k {
	case adminv1.PrincipalKind_PRINCIPAL_KIND_USER:
		return capability.PrincipalUser
	case adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT:
		return capability.PrincipalAgent
	case adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE:
		return capability.PrincipalService
	default:
		return ""
	}
}

// issued answers Issue and Delegate: the capability, its JWT, and the same
// capability as a Biscuit its holder can narrow offline.
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

func (h *Handler) issued(cap *capability.Capability, token string) (*connect.Response[adminv1.CapabilityServiceIssueResponse], error) {
	bisc, err := h.issuer.Biscuit(cap)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&adminv1.CapabilityServiceIssueResponse{
		Capability: capabilityToProto(cap),
		Token:      token,
		Biscuit:    bisc,
	}), nil
}

func protoToCaveats(c *adminv1.CapabilityCaveats) (capability.Caveats, error) {
	if c == nil {
		return capability.Caveats{}, nil
	}
	if err := apiutil.RefuseRemovedFields(c); err != nil {
		return capability.Caveats{}, err
	}
	budget, err := apiutil.AmountFromMicros("max_budget", c.GetMaxBudgetMicros())
	if err != nil {
		return capability.Caveats{}, err
	}
	// Empty unit_code on the wire ⇒ default to USD server-side.
	// Old clients (pre-currency rename) never set the field; new
	// clients may pin EUR/UAH/GBP/UNIT explicitly.
	unit := c.GetUnitCode()
	if unit == "" {
		unit = capability.DefaultUnitCode
	}
	out := capability.Caveats{
		ResourcePrefixes:       c.GetResourcePrefixes(),
		ResourceURIs:           c.GetResourceUris(),
		MaxRequests:            int(c.GetMaxRequests()),
		MaxBudgetAmount:        budget,
		UnitCode:               unit,
		AllowTaintedRead:       c.GetAllowTaintedRead(),
		IdempotencyKeyRequired: c.GetIdempotencyKeyRequired(),
		SourceIPCIDR:           c.GetSourceIpCidr(),
	}
	for _, op := range c.GetOps() {
		out.Ops = append(out.Ops, capability.Op(op))
	}
	return out, nil
}

func principalToProto(p capability.Principal) *adminv1.CapabilityPrincipal {
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
		if p.Agent.ParentAgentID != uuid.Nil {
			out.ParentAgentId = p.Agent.ParentAgentID.String()
		}
	}
	return out
}

func principalKindToProto(t capability.PrincipalType) adminv1.PrincipalKind {
	switch t {
	case capability.PrincipalUser:
		return adminv1.PrincipalKind_PRINCIPAL_KIND_USER
	case capability.PrincipalAgent:
		return adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT
	case capability.PrincipalService:
		return adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE
	default:
		return adminv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
	}
}

func caveatsToProto(c capability.Caveats) *adminv1.CapabilityCaveats {
	unit := c.UnitCode
	if unit == "" {
		unit = capability.DefaultUnitCode
	}
	out := &adminv1.CapabilityCaveats{
		ResourcePrefixes:       c.ResourcePrefixes,
		ResourceUris:           c.ResourceURIs,
		MaxRequests:            safecast.Int32(c.MaxRequests),
		MaxBudgetMicros:        proto.Int64(apiutil.Micros(c.MaxBudgetAmount)),
		UnitCode:               unit,
		AllowTaintedRead:       c.AllowTaintedRead,
		IdempotencyKeyRequired: c.IdempotencyKeyRequired,
		SourceIpCidr:           c.SourceIPCIDR,
	}
	for _, op := range c.Ops {
		out.Ops = append(out.Ops, string(op))
	}
	return out
}

func capabilityToProto(c *capability.Capability) *adminv1.Capability {
	out := &adminv1.Capability{
		Id:              c.ID.String(),
		Issuer:          c.Issuer,
		Subject:         principalToProto(c.Subject),
		Audience:        c.Audience,
		Caveats:         caveatsToProto(c.Caveats),
		IssuedAt:        timestamppb.New(c.IssuedAt),
		ExpiresAt:       timestamppb.New(c.ExpiresAt),
		Generation:      c.Generation,
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
