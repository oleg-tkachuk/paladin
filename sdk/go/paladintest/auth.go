package paladintest

import (
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// Option configures a fake at New or Start.
type Option func(*Server)

// WithStrictAuth makes the fake check credentials as the server's data plane
// does, instead of serving every call. A call must carry a credential the
// fake issued — IssueBearerToken, IssueAPIToken or IssueCapability — that
// was not revoked, and every resource it names must be in that credential's
// tenant. The answers are the server's, codes and messages, with no ErrorInfo
// reason, as the server's authentication sends none:
//
//   - no credential, or an Authorization that is not a bearer token:
//     Unauthenticated;
//   - a bearer token or an API token the fake did not issue, or one that was
//     revoked: Unauthenticated — a revoked bearer token answers as the
//     server does an expired one, since the server revokes none;
//   - a capability the fake did not issue, or one that was revoked:
//     PermissionDenied, as the server answers every capability it cannot
//     verify;
//   - a name or parent in another tenant: PermissionDenied; an upload of
//     another tenant: PermissionDenied.
//
// The fake checks only that: it verifies no signature, no Biscuit, no
// caveat, scope, audience or expiry, and it has no platform admin who acts
// in another tenant. Credentials are checked before FailRPC's failures.
func WithStrictAuth() Option {
	return func(s *Server) { s.strictAuth = true }
}

// Prefixes of the credentials the fake issues. An API token carries the
// server's prefix, so a client tells it from a bearer token as it does
// against the server; the others are the fake's own.
const (
	apiTokenPrefix    = "paladin_pat_"
	bearerTokenPrefix = "paladintest_jwt_"
	capabilityPrefix  = "paladintest_cap_"
	// capabilityScheme is the Authorization scheme of a capability, the
	// alternative to HeaderCapability.
	capabilityScheme = "capability "
	bearerScheme     = "Bearer "
)

// credentialKind is what a credential the fake issued is.
type credentialKind int

const (
	kindBearer credentialKind = iota + 1
	kindAPIToken
	kindCapability
)

// credential is one the fake issued: its kind, its tenant, and whether it
// was revoked.
type credential struct {
	kind    credentialKind
	tenant  string
	revoked bool
}

// The server's answers to a credential it refuses, word for word.
var (
	errMissingAuthorization = errors.New("missing Authorization header")
	errExpectedBearer       = errors.New("expected Bearer token")
	errEmptyToken           = errors.New("empty token")
	errJWTMalformed         = errors.New("jwt: malformed token")
	errJWTExpired           = errors.New("jwt: token expired")
	errAPITokenNotFound     = errors.New("api_token: token not found")
	errAPITokenRevoked      = errors.New("api_token: token revoked")
	errCapabilityInvalid    = errors.New("capability: invalid signature")
	errCapabilityRevoked    = errors.New("capability: revoked")
	errTenantMismatch       = errors.New("URL tenant does not match token tenant")
	errNamesSpanTenants     = errors.New("resource names in one request must name the same tenant")
	errUploadTenantMismatch = errors.New("tenant mismatch")
)

// Fields of a request message that name a resource, whose tenant a
// credential must hold.
var namingFields = []protoreflect.Name{"name", "parent"}

// uploadIDField names a multipart upload, whose object's tenant a credential
// must hold.
const uploadIDField protoreflect.Name = "upload_id"

// IssueBearerToken returns a bearer token for tenant — Tenant() for the
// fake's own — as an OIDC JWT is, for paladin.WithBearerToken or a
// TokenSource.
func (s *Server) IssueBearerToken(tenant string) string {
	return s.issue(kindBearer, bearerTokenPrefix, tenant)
}

// IssueAPIToken returns an API token for tenant, for paladin.WithAPIToken or
// paladin.WithBearerToken.
func (s *Server) IssueAPIToken(tenant string) string {
	return s.issue(kindAPIToken, apiTokenPrefix, tenant)
}

// IssueCapability returns a capability for tenant, for
// paladin.WithCapability or a WithCapabilitySource that mints one per call.
// It is the fake's own token, not a Biscuit: the fake checks only that it
// issued it, did not revoke it, and that it names the tenant.
func (s *Server) IssueCapability(tenant string) string {
	return s.issue(kindCapability, capabilityPrefix, tenant)
}

func (s *Server) issue(kind credentialKind, prefix, tenant string) string {
	token := prefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credentials[token] = &credential{kind: kind, tenant: tenant}
	return token
}

// Revoke revokes a credential the fake issued: under WithStrictAuth, a call
// that carries it is refused from then on — Unauthenticated for a bearer or
// API token, PermissionDenied for a capability, as the server answers. A
// token the fake did not issue is ignored.
func (s *Server) Revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.credentials[token]; ok {
		c.revoked = true
	}
}

// authenticate is the server's answer to a call's credentials under
// WithStrictAuth, nil when it lets the call through.
func (s *Server) authenticate(header http.Header, msg proto.Message) error {
	tenant, err := s.principalTenant(header)
	if err != nil {
		return err
	}
	return s.authorizeTenant(tenant, msg)
}

// principalTenant is the tenant of the call's credential, found as the
// server's interceptors find it: an API token first, then a capability,
// then a bearer token.
func (s *Server) principalTenant(header http.Header) (string, error) {
	authz := header.Get(paladin.HeaderAuthorization)
	if token := apiToken(header.Get(paladin.HeaderAPIToken), authz); token != "" {
		return s.verify(token, kindAPIToken, connect.CodeUnauthenticated, errAPITokenNotFound, errAPITokenRevoked)
	}
	if token := capabilityToken(header.Get(paladin.HeaderCapability), authz); token != "" {
		return s.verify(token, kindCapability, connect.CodePermissionDenied, errCapabilityInvalid, errCapabilityRevoked)
	}
	switch {
	case authz == "":
		return "", connect.NewError(connect.CodeUnauthenticated, errMissingAuthorization)
	case !strings.HasPrefix(authz, bearerScheme):
		return "", connect.NewError(connect.CodeUnauthenticated, errExpectedBearer)
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, bearerScheme))
	if token == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, errEmptyToken)
	}
	return s.verify(token, kindBearer, connect.CodeUnauthenticated, errJWTMalformed, errJWTExpired)
}

// apiToken is the API token a call carries: the X-header, else a bearer
// Authorization with the API token prefix; "" when it carries none.
func apiToken(xHeader, authz string) string {
	if strings.HasPrefix(xHeader, apiTokenPrefix) {
		return xHeader
	}
	if token, ok := strings.CutPrefix(authz, bearerScheme); ok && strings.HasPrefix(token, apiTokenPrefix) {
		return token
	}
	return ""
}

// capabilityToken is the capability a call carries: the X-header, else an
// Authorization with the capability scheme; "" when it carries none.
func capabilityToken(xHeader, authz string) string {
	if xHeader != "" {
		return xHeader
	}
	token, _ := strings.CutPrefix(authz, capabilityScheme)
	if token == authz {
		return ""
	}
	return strings.TrimSpace(token)
}

// verify is the tenant of token, a credential of kind, or code with unknown
// or revoked.
func (s *Server) verify(token string, kind credentialKind, code connect.Code, unknown, revoked error) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.credentials[token]
	switch {
	case !ok || c.kind != kind:
		return "", connect.NewError(code, unknown)
	case c.revoked:
		return "", connect.NewError(code, revoked)
	}
	return c.tenant, nil
}

// authorizeTenant refuses a call that names a resource outside tenant: its
// name or parent, or the object of the multipart upload it names.
func (s *Server) authorizeTenant(tenant string, msg proto.Message) error {
	if msg == nil {
		return nil
	}
	fields := msg.ProtoReflect().Descriptor().Fields()
	named := ""
	for _, name := range namingFields {
		f := fields.ByName(name)
		if f == nil || f.Kind() != protoreflect.StringKind || f.IsList() {
			continue
		}
		t, ok := tenantOf(msg.ProtoReflect().Get(f).String())
		if !ok {
			continue // not a resource name: the RPC refuses it itself
		}
		if named != "" && t != named {
			return connect.NewError(connect.CodePermissionDenied, errNamesSpanTenants)
		}
		named = t
	}
	if named != "" && named != tenant {
		return connect.NewError(connect.CodePermissionDenied, errTenantMismatch)
	}
	if f := fields.ByName(uploadIDField); f != nil && f.Kind() == protoreflect.StringKind {
		if t, ok := s.uploadTenant(msg.ProtoReflect().Get(f).String()); ok && t != tenant {
			return connect.NewError(connect.CodePermissionDenied, errUploadTenantMismatch)
		}
	}
	return nil
}

// tenantOf is the tenant a resource name or a collection names.
func tenantOf(name string) (string, bool) {
	if n, err := paladin.ParseObjectName(name); err == nil {
		return n.Tenant, true
	}
	if c, err := paladin.ParseCollectionName(name); err == nil {
		return c.Tenant, true
	}
	return "", false
}

// uploadTenant is the tenant of the object a multipart upload registered.
func (s *Server) uploadTenant(uploadID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up, ok := s.uploads[uploadID]
	if !ok {
		return "", false
	}
	o, ok := s.objects[up.name]
	if !ok {
		return "", false
	}
	return o.msg.GetTenantId(), true
}
