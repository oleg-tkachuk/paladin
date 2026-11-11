package paladintest

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/oleg-tkachuk/limes"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// What WithStrictAuth checks of a capability that CapabilityService issued or
// delegated, as the server does: the plane its audience names, its request
// ceiling and its ancestors', and each call's operation and resource against
// its caveats. The refusals are the server's, code, reason and message, and
// come in the server's order: the audience when the capability is verified,
// before its revocation; the request ceiling next, before the request is
// validated; the operation and resource in the RPC, after what it looks up.
//
// A capability from IssueCapability carries no caveats, and is checked for its
// tenant and its revocation alone, as before. The budget ceiling is not
// checked: the server charges a request only when the deployment sets a
// price per request, which it does not by default, so no request spends any.

// libraryPrefix is what the server's capability library begins its errors
// with, which the server strips from every message it sends
// (backend/internal/middleware/scrub.go).
const libraryPrefix = "limes: "

// capabilityReasonPrefix starts the name of every capability refusal's
// ErrorReason, which the rest of names: limes's reason, in capitals.
const capabilityReasonPrefix = "ERROR_REASON_CAPABILITY_"

// problemDetailSeparator joins a limes problem's title to its detail, as
// the server joins them.
const problemDetailSeparator = ": "

// refusalMessage is what the server tells a caller of err: the fixed title
// of its limes problem when it is the library's, whose own text is for the
// server's log, followed by the problem's detail when limes gives one
// (backend/internal/auth capabilityMessage); err's text without the
// library's name otherwise.
func refusalMessage(err error) string {
	p, ok := limes.ProblemOf(err)
	if !ok || p.Title == "" {
		return strings.ReplaceAll(err.Error(), libraryPrefix, "")
	}
	if p.Detail == "" {
		return p.Title
	}
	return p.Title + problemDetailSeparator + p.Detail
}

// capabilityRefusal is the server's answer to a refusal of the capability:
// code, refusalMessage, and an ErrorInfo naming the refusal's reason and,
// under paladin.ErrorMetadataLimesCode, its limes code.
func capabilityRefusal(code connect.Code, err error) *connect.Error {
	e := connect.NewError(code, refusalMessage(err)).WithCause(err)
	r, ok := limes.ReasonOf(err)
	if !ok {
		return e
	}
	value, ok := commonv1.ErrorReason_value[capabilityReasonPrefix+strings.ToUpper(string(r))]
	if !ok {
		return e
	}
	info := &errdetails.ErrorInfo{Reason: commonv1.ErrorReason(value).String(), Domain: paladin.ErrorDomain}
	if code, ok := limes.CodeOf(err); ok {
		info.Metadata = map[string]string{paladin.ErrorMetadataLimesCode: string(code)}
	}
	detail, derr := connectproto.NewErrorDetail(info)
	if derr != nil {
		panic(fmt.Sprintf("paladintest: %v", derr)) // an ErrorInfo always marshals
	}
	return e.WithDetail(detail)
}

// Procedure prefixes of the planes whose verifier checks a capability.
const (
	dataProcedures  = "/paladin.data.v1."
	adminProcedures = "/paladin.admin.v1."
)

// planeOf is the plane whose verifier checks a capability presented to
// procedure; false for one no capability verifier stands in front of.
func planeOf(procedure string) (paladin.Plane, bool) {
	switch {
	case strings.HasPrefix(procedure, dataProcedures):
		return paladin.PlaneData, true
	case strings.HasPrefix(procedure, adminProcedures):
		return paladin.PlaneAdmin, true
	}
	return "", false
}

// audienceRefusal is the verifier's refusal of a capability whose audience
// does not name plane, nil when it does.
func audienceRefusal(audience []string, plane paladin.Plane) error {
	if slices.Contains(audience, string(plane)) {
		return nil
	}
	return capabilityRefusal(connect.CodeUnauthenticated, fmt.Errorf("%w: token audience %v lacks %q", limes.ErrAudienceMismatch, audience, string(plane)))
}

// requestCeiling is the request ceiling caveats set; one that sets none
// counts without a limit.
func requestCeiling(caveats *adminv1.CapabilityCaveats) limes.RequestCeiling {
	if r, ok := caveats.GetRequestCeiling().(*adminv1.CapabilityCaveats_MaxRequests); ok {
		return limes.RequestCeiling{Max: r.MaxRequests}
	}
	return limes.RequestCeiling{Unlimited: true}
}

// limesCaveats is what of caveats limes checks an operation and its resource
// against.
func limesCaveats(caveats *adminv1.CapabilityCaveats) limes.Caveats {
	ops := make([]limes.Op, 0, len(caveats.GetOps()))
	for _, op := range caveats.GetOps() {
		ops = append(ops, limes.Op(op))
	}
	return limes.Caveats{
		Ops:                    ops,
		ResourcePrefixes:       caveats.GetResourcePrefixes(),
		ResourceURIs:           caveats.GetResourceUris(),
		AllowTaintedRead:       caveats.GetAllowTaintedRead(),
		IdempotencyKeyRequired: caveats.GetIdempotencyKeyRequired(),
	}
}

// opEffects is the effect the server declares for each of the data plane's
// own operations; one absent here is a write, and limes's own resolve their
// own (backend/internal/auth/capability_ops.go).
var opEffects = map[limes.Op]limes.Effect{
	limes.Op(paladin.OpObjectPresign): limes.EffectRead,
}

// presented is the capability a call carries, as CapabilityService issued it.
type presented struct {
	rec *capabilityRecord
	// narrowed is a copy's capability as its blocks narrow it; nil for the
	// capability's own token or Biscuit.
	narrowed *limes.Capability
}

// presentedLocked is the capability a call carries for plane — any plane
// when "" — and nil when it carries none CapabilityService issued. A copy
// of its Biscuit that the library refuses is refused, as the server's
// verifier refuses it, before anything else is looked at; so is any other
// capability the fake did not issue, which the server hands its verifier
// too.
func (s *Server) presentedLocked(header http.Header, plane paladin.Plane) (*presented, error) {
	token := capabilityToken(header.Get(paladin.HeaderCapability), header.Get(paladin.HeaderAuthorization))
	if token == "" {
		return nil, nil
	}
	if c, ok := s.credentials[token]; ok {
		if c.capability == "" {
			return nil, nil
		}
		return &presented{rec: s.capabilities[c.capability]}, nil
	}
	id, narrowed, err := s.seal.open(token, plane)
	if err != nil {
		return nil, err
	}
	return &presented{rec: s.capabilities[id], narrowed: &narrowed}, nil
}

// caveats is what a call with p is checked against: a copy's, narrowed, or
// the capability's own.
func (p *presented) caveats() limes.Caveats {
	if p.narrowed != nil {
		return p.narrowed.Caveats
	}
	return limesCaveats(p.rec.msg.GetCaveats())
}

// noResource is the resource of an operation bound to none, which a
// resource-restricted capability refuses.
const noResource = ""

// objectURI is the resource an object is to a capability.
func objectURI(o *object) string {
	return paladin.ObjectResource(o.msg.GetTenantId(), o.msg.GetCollection(), o.msg.GetKey())
}

// childURI is the resource a key under the collection parent names, or the
// collection's own prefix for an empty key, as a listing asks for it.
func childURI(parent, key string) string {
	c, err := paladin.ParseCollectionName(parent)
	if err != nil {
		return noResource // the RPC refuses the name itself first
	}
	return paladin.ObjectResource(c.Tenant, c.Collection, key)
}

// uploadURILocked is the resource of the object a multipart upload writes.
func (s *Server) uploadURILocked(up *multipart) string {
	if o, ok := s.objects[up.name]; ok {
		return objectURI(o)
	}
	return noResource
}

// idempotencyKeyCarrier is a request that names its idempotency key in its
// body, which the server reads as it reads the header.
type idempotencyKeyCarrier interface {
	GetIdempotencyKey() string
}

// hasIdempotencyKey reports whether a call carries an idempotency key, in its
// header or its request.
func hasIdempotencyKey(header http.Header, req any) bool {
	if header.Get(paladin.HeaderIdempotencyKey) != "" {
		return true
	}
	c, ok := req.(idempotencyKeyCarrier)
	return ok && c.GetIdempotencyKey() != ""
}

// allowLocked is the server's check that the capability a call carries allows
// ops on resource, in order: nil when the call carries none CapabilityService
// issued, or the fake is not strict. resource is the object's URI
// (objectURI), a collection's prefix for a listing (childURI), or noResource.
func (s *Server) allowLocked(header http.Header, req any, resource string, ops ...paladin.Op) error {
	if !s.strictAuth {
		return nil
	}
	p, err := s.presentedLocked(header, "")
	if err != nil || p == nil {
		return err
	}
	caveats := p.caveats()
	for _, op := range ops {
		lop := limes.Op(op)
		req := limes.CheckRequest{
			Op:                lop,
			Effect:            opEffects[lop],
			Resource:          resource,
			HasIdempotencyKey: hasIdempotencyKey(header, req),
		}
		if err := caveats.Check(req); err != nil {
			return capabilityRefusal(connect.CodePermissionDenied, err)
		}
	}
	return nil
}
