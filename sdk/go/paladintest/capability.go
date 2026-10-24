package paladintest

import (
	"context"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// CapabilityService, as the fake serves it: Issue, Delegate, Revoke, Get and
// List.
//
// It holds a capability to the server's rules where a consumer's tests meet
// them: an operation the server refuses — a bare "presign" — is
// InvalidArgument; a capability is answered with a Biscuit unless it is bound
// to a key (KEY_BOUND) or valid only from a later not_before
// (NOT_YET_VALID), share or no share; require_biscuit refuses the others
// before anything is issued; Delegate narrows only the capability presented
// in HeaderCapability, which must allow share, be the parent_id named and not
// be a Biscuit copy, and grants no operation the parent lacks.
//
// The fake checks only that. Its token and its Biscuit are the fake's own
// credentials, not a JWT and a Biscuit — paladin.Attenuate refuses the
// fake's Biscuit — and of the lifetime it checks only that a not_before
// falls before the expiry. Under WithStrictAuth a call is held to the
// capability's planes, request ceiling, operations and resources
// (caveats.go); its budget and copy quota are not counted. Both credentials are
// accepted under WithStrictAuth for the subject's tenant until the
// capability is revoked; with WithStrictAuth, Issue for another tenant than
// the caller's is refused, as the server refuses it to all but a platform
// role.

// capabilityService serves CapabilityService on the fake. A type of its own,
// since Server.Revoke revokes a credential by its token.
type capabilityService struct {
	paladinadminv1connect.UnimplementedCapabilityServiceHandler
	*Server
}

// Prefixes and names of what the fake issues for a capability.
const (
	// biscuitPrefix marks the fake's stand-in for a capability's Biscuit.
	biscuitPrefix = "paladintest_biscuit_"
	// capabilityIssuer is the issuer the fake's capabilities name.
	capabilityIssuer = "paladintest"
	// DefaultCapabilityTTL is how long a capability lives when Issue names no
	// TTL: the server's default_ttl.
	DefaultCapabilityTTL = 15 * time.Minute
)

// The server's answers, word for word, where the fake gives them.
const (
	msgNoRequestCeiling = "caveats.request_ceiling: set max_requests, or unlimited_requests for no limit"
	msgNoBudgetCeiling  = "caveats.budget_ceiling: set max_budget, or unlimited_budget for no limit"
	msgIssueElsewhere   = "issuing a capability for another tenant requires platform.admin or platform.capability-issuer"
	msgNotPresented     = "capability: delegation narrows a presented capability; send it in the " + paladin.HeaderCapability + " header"
	msgLacksShare       = "capability: caller lacks OpShare"
	msgNotOwnParent     = "capability: parent_id must equal caller's own capability id"
	msgBiscuitDelegates = "capability: a Biscuit copy cannot delegate; attenuate it instead"
)

// biscuitRefusals says, for require_biscuit, why each status leaves a
// capability without its Biscuit, as the server says it.
var biscuitRefusals = map[adminv1.BiscuitStatus]string{
	adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND:     "it is bound to a key, and only its holder can prove it; present the token",
	adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID: "it is valid only from a later not_before",
}

// capabilityRecord is one capability the fake issued.
type capabilityRecord struct {
	msg *adminv1.Capability
	// credentials are its token and, when it has one, its Biscuit.
	credentials []string
	children    []string
	// revocation is its own revocation entry, the first one written; nil
	// while it has none.
	revocation *adminv1.CapabilityRevocation
	// requests is how many requests it was presented to, ancestors counting
	// their descendants', as the server counts them against a ceiling.
	requests int64
}

// biscuitStatusOf is the server's: a key-bound capability and one valid only
// later go without a Biscuit; its ops play no part.
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

// requireBiscuit is require_biscuit's refusal, nil when status is ISSUED.
func requireBiscuit(status adminv1.BiscuitStatus) error {
	if status == adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED {
		return nil
	}
	return withReason(connect.Errorf(connect.CodeFailedPrecondition,
		"require_biscuit: the capability cannot be sealed as a Biscuit (%s): %s", status, biscuitRefusals[status]))
}

// validOps refuses an operation the server refuses in a caveat. Caveats
// that name none are refused before, by the contract's own rule.
func validOps(ops []string) error {
	for _, op := range ops {
		if err := paladin.Op(op).Validate(); err != nil {
			// The server's message is what follows the SDK's own prefix.
			msg := strings.TrimPrefix(err.Error(), paladin.ErrInvalidArgument.Error()+": ")
			return withReason(connect.NewError(connect.CodeInvalidArgument, msg).WithCause(err))
		}
	}
	return nil
}

// Issue mints a capability for the request's subject.
func (s capabilityService) Issue(ctx context.Context, req *adminv1.CapabilityServiceIssueRequest) (*adminv1.CapabilityServiceIssueResponse, error) {
	tenant := req.GetSubject().GetTenantId()
	if s.strictAuth {
		caller, err := s.principalTenant(requestHeader(ctx))
		if err != nil {
			return nil, err
		}
		if caller != tenant {
			return nil, withReason(connect.NewError(connect.CodePermissionDenied, msgIssueElsewhere))
		}
	}
	caveats := req.GetCaveats()
	switch {
	case caveats.GetRequestCeiling() == nil:
		return nil, withReason(connect.NewError(connect.CodeInvalidArgument, msgNoRequestCeiling))
	case caveats.GetBudgetCeiling() == nil:
		return nil, withReason(connect.NewError(connect.CodeInvalidArgument, msgNoBudgetCeiling))
	}
	// require_biscuit first, then what limes refuses as it mints: the
	// server's order.
	now, nbf := time.Now(), notBefore(req.GetNotBefore())
	status := biscuitStatusOf(req.GetConfirmationJkt(), nbf, now)
	if req.GetRequireBiscuit() {
		if err := requireBiscuit(status); err != nil {
			return nil, err
		}
	}
	if err := validOps(caveats.GetOps()); err != nil {
		return nil, err
	}
	expires, err := lifetime(now, nbf, req.GetTtlSeconds())
	if err != nil {
		return nil, err
	}
	return s.mint(&adminv1.Capability{
		Subject:         proto.CloneOf(req.GetSubject()),
		Audience:        slices.Clone(req.GetAudience()),
		Caveats:         proto.CloneOf(caveats),
		ConfirmationJkt: req.GetConfirmationJkt(),
	}, now, nbf, expires, status, ""), nil
}

// Delegate narrows the capability the call presents.
func (s capabilityService) Delegate(ctx context.Context, req *adminv1.CapabilityServiceDelegateRequest) (*adminv1.CapabilityServiceIssueResponse, error) {
	header := requestHeader(ctx)
	presented := capabilityToken(header.Get(paladin.HeaderCapability), header.Get(paladin.HeaderAuthorization))
	if presented == "" {
		return nil, withReason(connect.NewError(connect.CodeFailedPrecondition, msgNotPresented))
	}
	s.mu.Lock()
	cred, ok := s.credentials[presented]
	var parent *capabilityRecord
	if ok {
		parent = s.capabilities[cred.capability]
	}
	s.mu.Unlock()
	switch {
	case !ok || parent == nil:
		return nil, connect.NewError(connect.CodePermissionDenied, errCapabilityInvalid.Error()).WithCause(errCapabilityInvalid)
	case cred.revoked:
		return nil, connect.NewError(connect.CodePermissionDenied, errCapabilityRevoked.Error()).WithCause(errCapabilityRevoked)
	}
	parentCaveats := parent.msg.GetCaveats()
	switch {
	case !slices.Contains(parentCaveats.GetOps(), string(paladin.OpShare)):
		return nil, withReason(connect.NewError(connect.CodePermissionDenied, msgLacksShare))
	case parent.msg.GetId() != req.GetParentId():
		return nil, withReason(connect.NewError(connect.CodePermissionDenied, msgNotOwnParent))
	case cred.biscuit:
		return nil, withReason(connect.NewError(connect.CodeFailedPrecondition, msgBiscuitDelegates))
	}
	jkt := req.GetConfirmationJkt()
	if jkt == "" {
		jkt = parent.msg.GetConfirmationJkt()
	}
	// require_biscuit first, then what limes refuses as it mints: the
	// server's order.
	now, nbf := time.Now(), notBefore(req.GetNotBefore())
	status := biscuitStatusOf(jkt, nbf, now)
	if req.GetRequireBiscuit() {
		if err := requireBiscuit(status); err != nil {
			return nil, err
		}
	}
	caveats := parentCaveats
	if c := req.GetCaveats(); c != nil {
		if err := validOps(c.GetOps()); err != nil {
			return nil, err
		}
		for _, op := range c.GetOps() {
			if !slices.Contains(parentCaveats.GetOps(), op) {
				return nil, withReason(connect.Errorf(connect.CodePermissionDenied,
					"delegation too wide: op %q is not granted to the parent", op))
			}
		}
		caveats = c
	}
	expires, err := lifetime(now, nbf, req.GetTtlSeconds())
	if err != nil {
		return nil, err
	}
	subject := parent.msg.GetSubject()
	if req.GetSubject() != nil {
		subject = req.GetSubject()
	}
	audience := parent.msg.GetAudience()
	if len(req.GetAudience()) > 0 {
		audience = req.GetAudience()
	}
	return s.mint(&adminv1.Capability{
		Subject:         proto.CloneOf(subject),
		Audience:        slices.Clone(audience),
		Caveats:         proto.CloneOf(caveats),
		ConfirmationJkt: jkt,
	}, now, nbf, expires, status, parent.msg.GetId()), nil
}

// Revoke revokes a capability the fake issued — its token and its Biscuit —
// and, with cascade_children, every one delegated from it. An id the fake did
// not issue is NotFound, as the server answers one the caller cannot see.
func (s capabilityService) Revoke(_ context.Context, req *adminv1.CapabilityServiceRevokeRequest) (*adminv1.CapabilityServiceRevokeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.capabilities[req.GetId()]
	if !ok {
		return nil, withReason(connect.Errorf(connect.CodeNotFound, "capability %s: not found", req.GetId()))
	}
	already := rec.revocation != nil
	before := s.dispositionOf(rec, time.Now())
	s.revokeCapability(req.GetId(), req.GetCascadeChildren(), &adminv1.CapabilityRevocation{
		RevokedAt: timestamppb.Now(), Reason: req.GetReason(), Cascade: req.GetCascadeChildren(),
	})
	// The first entry stands, as the server keeps it.
	return &adminv1.CapabilityServiceRevokeResponse{
		AlreadyRevoked: already,
		Revocation:     proto.CloneOf(rec.revocation),
		Before:         before,
	}, nil
}

// revokeCapability revokes id's credentials, and its children's when
// cascade, giving each without an entry of its own entry. A nil entry
// revokes the credentials alone. The caller holds mu.
func (s *Server) revokeCapability(id string, cascade bool, entry *adminv1.CapabilityRevocation) {
	rec := s.capabilities[id]
	for _, token := range rec.credentials {
		s.credentials[token].revoked = true
	}
	if entry != nil && rec.revocation == nil {
		rec.revocation = proto.CloneOf(entry)
	}
	if cascade {
		for _, child := range rec.children {
			s.revokeCapability(child, cascade, entry)
		}
	}
}

// stoppedBy is the entry that revokes rec: its own, or its nearest revoked
// ancestor's; nil for one with none. The caller holds mu.
func (s *Server) stoppedBy(rec *capabilityRecord) *adminv1.CapabilityStoppedBy {
	for r := rec; r != nil; r = s.capabilities[r.msg.GetParentId()] {
		if r.revocation != nil {
			return &adminv1.CapabilityStoppedBy{CapabilityId: r.msg.GetId(), Revocation: proto.CloneOf(r.revocation)}
		}
	}
	return nil
}

// Get answers a capability the fake issued, with its disposition as List
// gives it; any other id is NotFound.
func (s capabilityService) Get(_ context.Context, req *adminv1.CapabilityServiceGetRequest) (*adminv1.CapabilityServiceGetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.capabilities[req.GetId()]
	if !ok {
		return nil, withReason(connect.Errorf(connect.CodeNotFound, "capability %s: not found", req.GetId()))
	}
	c := proto.CloneOf(rec.msg)
	c.Disposition = s.dispositionOf(rec, time.Now())
	out := &adminv1.CapabilityServiceGetResponse{Capability: c, Revocation: proto.CloneOf(rec.revocation)}
	if c.GetDisposition() == adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED {
		out.StoppedBy = s.stoppedBy(rec)
	}
	return out, nil
}

// lifetime is when a capability issued at now for ttlSeconds — the default
// when 0 — expires, as the server reckons it: ttl after it is issued. A
// not-before nbf that is not before then is InvalidArgument.
func lifetime(now, nbf time.Time, ttlSeconds int64) (time.Time, error) {
	ttl := DefaultCapabilityTTL
	if ttlSeconds > 0 {
		ttl = time.Duration(ttlSeconds) * time.Second
	}
	until := now.Add(ttl)
	if !nbf.IsZero() && !nbf.Before(until) {
		return time.Time{}, withReason(connect.Errorf(connect.CodeInvalidArgument,
			"invalid request: NotBefore %s is not before ExpiresAt %s", nbf.UTC(), until.UTC()))
	}
	return until, nil
}

// notBefore is t, or the zero time when it is unset.
func notBefore(t *timestamppb.Timestamp) time.Time {
	if !t.IsValid() {
		return time.Time{}
	}
	return t.AsTime()
}

// mint records c, issued at now and valid from nbf until expires, and
// answers it with its token and, when status is ISSUED, its Biscuit.
func (s *Server) mint(c *adminv1.Capability, now, nbf, expires time.Time, status adminv1.BiscuitStatus, parent string) *adminv1.CapabilityServiceIssueResponse {
	// A UUIDv7, as the server's: List pages in id order, which is issue order.
	c.Id = uuid.Must(uuid.NewV7()).String()
	c.Issuer = capabilityIssuer
	c.ParentId = parent
	c.IssuedAt = timestamppb.New(now)
	c.ExpiresAt = timestamppb.New(expires)
	if !nbf.IsZero() {
		c.NotBefore = timestamppb.New(nbf)
	}
	tenant := c.GetSubject().GetTenantId()
	res := &adminv1.CapabilityServiceIssueResponse{BiscuitStatus: status}
	res.Token = s.issueFor(c.GetId(), capabilityPrefix, tenant, false)
	if status == adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED {
		res.Biscuit = s.issueFor(c.GetId(), biscuitPrefix, tenant, true)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := &capabilityRecord{msg: c, credentials: []string{res.GetToken()}}
	if res.GetBiscuit() != "" {
		rec.credentials = append(rec.credentials, res.GetBiscuit())
	}
	s.capabilities[c.GetId()] = rec
	if p, ok := s.capabilities[parent]; ok {
		p.children = append(p.children, c.GetId())
	}
	res.Capability = proto.CloneOf(c)
	return res
}

// issueFor is a capability credential of the capability id, its Biscuit when
// biscuit.
func (s *Server) issueFor(id, prefix, tenant string, biscuit bool) string {
	token := s.issue(kindCapability, prefix, tenant)
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.credentials[token]
	c.capability, c.biscuit = id, biscuit
	return token
}
