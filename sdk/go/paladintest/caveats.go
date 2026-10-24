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

// capabilityRefusal is the server's answer to a refusal of the capability:
// code, the message without the library's name, and an ErrorInfo naming the
// refusal's reason.
func capabilityRefusal(code connect.Code, err error) *connect.Error {
	e := connect.NewError(code, strings.ReplaceAll(err.Error(), libraryPrefix, "")).WithCause(err)
	r, ok := limes.ReasonOf(err)
	if !ok {
		return e
	}
	value, ok := commonv1.ErrorReason_value[capabilityReasonPrefix+strings.ToUpper(string(r))]
	if !ok {
		return e
	}
	detail, derr := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Reason: commonv1.ErrorReason(value).String(), Domain: paladin.ErrorDomain})
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
	return capabilityRefusal(connect.CodePermissionDenied, fmt.Errorf("%w: token audience %v lacks %q", limes.ErrAudienceMismatch, audience, string(plane)))
}

// requestLimitAdmits reports whether a capability with caveats admits its
// next request, the count-th.
func requestLimitAdmits(caveats *adminv1.CapabilityCaveats, count int64) bool {
	if caveats.GetUnlimitedRequests() {
		return true
	}
	if _, ok := caveats.GetRequestCeiling().(*adminv1.CapabilityCaveats_MaxRequests); !ok {
		return true
	}
	return count <= caveats.GetMaxRequests()
}

// bumpRequestsLocked counts a request against rec and each of its ancestors,
// or refuses it, counting nothing, when one of them has no room for it: rec
// first, then its ancestors from the nearest.
func (s *Server) bumpRequestsLocked(rec *capabilityRecord) error {
	chain := []*capabilityRecord{}
	for r := rec; r != nil; r = s.capabilities[r.msg.GetParentId()] {
		chain = append(chain, r)
	}
	for i, r := range chain {
		if requestLimitAdmits(r.msg.GetCaveats(), r.requests+1) {
			continue
		}
		err := limes.ErrRequestLimitExceeded
		if i > 0 {
			err = fmt.Errorf("%w: ancestor %s", limes.ErrRequestLimitExceeded, r.msg.GetId())
		}
		return capabilityRefusal(connect.CodeResourceExhausted, err)
	}
	for _, r := range chain {
		r.requests++
	}
	return nil
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

// presentedLocked is the record of the capability a call carries, nil when it
// carries none CapabilityService issued.
func (s *Server) presentedLocked(header http.Header) *capabilityRecord {
	token := capabilityToken(header.Get(paladin.HeaderCapability), header.Get(paladin.HeaderAuthorization))
	if token == "" {
		return nil
	}
	c, ok := s.credentials[token]
	if !ok || c.capability == "" {
		return nil
	}
	return s.capabilities[c.capability]
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
	rec := s.presentedLocked(header)
	if rec == nil {
		return nil
	}
	caveats := limesCaveats(rec.msg.GetCaveats())
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
