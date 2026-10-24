package paladintest_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// refusal is how the server refused a call: its code, and its message.
type refusal struct {
	code    connect.Code
	message string
	// reason is the ErrorInfo reason the server sends, none for most.
	reason commonv1.ErrorReason
}

// wantRefused fails the test unless err is the server's refusal, with the
// ErrorInfo reason it sends: none for its authentication.
func wantRefused(t *testing.T, err error, want refusal) {
	t.Helper()
	if connect.CodeOf(err) != want.code {
		t.Fatalf("err = %v, want %s", err, want.code)
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Message() != want.message {
		t.Errorf("message = %q, want the server's %q", cerr.Message(), want.message)
	}
	if got := paladin.Reason(err); got != want.reason {
		t.Errorf("reason = %s, want %s, as the server sends", got, want.reason)
	}
}

var (
	missingAuthorization = refusal{code: connect.CodeUnauthenticated, message: "missing Authorization header"}
	expectedBearer       = refusal{code: connect.CodeUnauthenticated, message: "expected Bearer token"}
	jwtMalformed         = refusal{code: connect.CodeUnauthenticated, message: "jwt: malformed token"}
	jwtExpired           = refusal{code: connect.CodeUnauthenticated, message: "jwt: token expired"}
	apiTokenNotFound     = refusal{code: connect.CodeUnauthenticated, message: "api_token: token not found"}
	apiTokenRevoked      = refusal{code: connect.CodeUnauthenticated, message: "api_token: token revoked"}
	// A capability the fake did not issue goes to the verifier, as on the
	// server, which cannot read "not-issued" as a Biscuit.
	capabilityNotIssued = refusal{connect.CodePermissionDenied, "invalid token: biscuit encoding: illegal base64 data at input byte 8",
		commonv1.ErrorReason_ERROR_REASON_CAPABILITY_INVALID_TOKEN}
	capabilityRevoked    = refusal{code: connect.CodePermissionDenied, message: "capability: revoked"}
	tenantMismatch       = refusal{code: connect.CodePermissionDenied, message: "URL tenant does not match token tenant"}
	uploadTenantMismatch = refusal{code: connect.CodePermissionDenied, message: "tenant mismatch"}
)

// Without WithStrictAuth the fake serves every call, as it always has: no
// credential, or one for another tenant.
func TestDefaultModeServesEveryCall(t *testing.T) {
	srv := paladintest.New(t)
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	for name, p := range map[string]*paladin.Paladin{
		"no credential":        srv.Connect(),
		"an unknown token":     srv.Connect(paladin.WithBearerToken("not-issued")),
		"another tenant's":     srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(uuid.NewString()))),
		"a revoked token":      srv.Connect(paladin.WithBearerToken(revoked(srv, srv.IssueBearerToken(srv.Tenant())))),
		"a basic credential":   srv.Connect(paladin.WithHeader(paladin.HeaderAuthorization, "Basic dXNlcjpwYXNz")),
		"a foreign capability": srv.Connect(paladin.WithCapability(srv.IssueCapability(uuid.NewString()))),
	} {
		if err := getObject(p, obj); err != nil {
			t.Errorf("%s: %v, want the object", name, err)
		}
	}
}

func revoked(srv *paladintest.Server, token string) string {
	srv.Revoke(token)
	return token
}

// Under WithStrictAuth each credential the fake issued, in each header the
// server reads it from, lets a call into its own tenant through.
func TestStrictAuthAcceptsAnIssuedCredential(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	apiToken := srv.IssueAPIToken(srv.Tenant())
	capability := srv.IssueCapability(srv.Tenant())
	for name, opts := range map[string][]paladin.Option{
		"a bearer token":                {paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))},
		"an API token in its header":    {paladin.WithAPIToken(apiToken)},
		"an API token as a bearer":      {paladin.WithBearerToken(apiToken)},
		"a capability in its header":    {paladin.WithCapability(capability)},
		"a capability in Authorization": {paladin.WithHeader(paladin.HeaderAuthorization, "capability "+capability)},
		"a capability minted per call":  {paladin.WithCapabilitySource(func(context.Context) string { return capability })},
		// The server's JWT gate steps aside for a capability, as the fake does.
		"a capability beside a bad bearer": {paladin.WithCapability(capability), paladin.WithBearerToken("not-issued")},
	} {
		if err := getObject(srv.Connect(opts...), obj); err != nil {
			t.Errorf("%s: %v, want the object", name, err)
		}
	}
}

// Under WithStrictAuth a whole upload and download goes through with a
// credential, as against the server.
func TestStrictAuthServesTheWorkflows(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	p := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant())))
	ctx := context.Background()
	body := bytes.Repeat([]byte("p"), 2*paladintest.PartSize+1)
	obj, err := paladin.Upload(ctx, p.Data, paladin.UploadInput{ContentType: testContentType,
		Parent: srv.Collection().String(), Key: "k", Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{MultipartThreshold: paladintest.PartSize})
	if err != nil {
		t.Fatal(err)
	}
	r, err := paladin.Download(ctx, p.Data, obj.GetName(), paladin.DownloadOptions{})
	if got := read(t, r, err); !bytes.Equal(got, body) {
		t.Errorf("downloaded %d bytes, want %d", len(got), len(body))
	}
}

// Under WithStrictAuth the fake refuses what the server refuses, with the
// server's code and message, and records the refused call.
func TestStrictAuthRefusesWhatTheServerRefuses(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	other := uuid.NewString()
	cases := []struct {
		name string
		opt  paladin.Option
		want refusal
	}{
		{"no credential", nil, missingAuthorization},
		{"a basic credential", paladin.WithHeader(paladin.HeaderAuthorization, "Basic dXNlcjpwYXNz"), expectedBearer},
		{"a bearer token not issued", paladin.WithBearerToken("not-issued"), jwtMalformed},
		{"an API token not issued", paladin.WithAPIToken("paladin_pat_notissued"), apiTokenNotFound},
		{"a capability not issued", paladin.WithCapability("not-issued"), capabilityNotIssued},
		{"a capability given as a bearer token", paladin.WithBearerToken(srv.IssueCapability(srv.Tenant())), jwtMalformed},
		{"another tenant's bearer token", paladin.WithBearerToken(srv.IssueBearerToken(other)), tenantMismatch},
		{"another tenant's API token", paladin.WithAPIToken(srv.IssueAPIToken(other)), tenantMismatch},
		{"another tenant's capability", paladin.WithCapability(srv.IssueCapability(other)), tenantMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts []paladin.Option
			if tc.opt != nil {
				opts = append(opts, tc.opt)
			}
			before := calls(srv, paladindatav1connect.ObjectServiceGetObjectProcedure)
			wantRefused(t, getObject(srv.Connect(opts...), obj), tc.want)
			if after := calls(srv, paladindatav1connect.ObjectServiceGetObjectProcedure); after != before+1 {
				t.Errorf("requests went %d → %d, want the refused call recorded", before, after)
			}
		})
	}
}

// A revoked credential is refused from the next call on: a bearer or API
// token as Unauthenticated, a capability as PermissionDenied — the server's
// codes.
func TestRevokeRefusesTheCredential(t *testing.T) {
	cases := []struct {
		name  string
		issue func(*paladintest.Server) string
		opt   func(string) paladin.Option
		want  refusal
	}{
		{"a bearer token", func(s *paladintest.Server) string { return s.IssueBearerToken(s.Tenant()) }, paladin.WithBearerToken, jwtExpired},
		{"an API token", func(s *paladintest.Server) string { return s.IssueAPIToken(s.Tenant()) }, paladin.WithAPIToken, apiTokenRevoked},
		{"a capability", func(s *paladintest.Server) string { return s.IssueCapability(s.Tenant()) }, paladin.WithCapability, capabilityRevoked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := paladintest.New(t, paladintest.WithStrictAuth())
			obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
			token := tc.issue(srv)
			p := srv.Connect(tc.opt(token))
			if err := getObject(p, obj); err != nil {
				t.Fatalf("before Revoke: %v", err)
			}
			srv.Revoke(token)
			wantRefused(t, getObject(p, obj), tc.want)
		})
	}
}

// A client that mints a fresh capability once the server refuses its own
// gets through again: the path Revoke exists to test.
func TestRevokeThenMintAgain(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	var (
		mu      sync.Mutex
		current = srv.IssueCapability(srv.Tenant())
	)
	p := srv.Connect(paladin.WithCapabilitySource(func(context.Context) string {
		mu.Lock()
		defer mu.Unlock()
		return current
	}))
	srv.Revoke(current)
	err := getObject(p, obj)
	wantRefused(t, err, capabilityRevoked)
	mu.Lock()
	current = srv.IssueCapability(srv.Tenant())
	mu.Unlock()
	if err := getObject(p, obj); err != nil {
		t.Errorf("with a fresh capability: %v", err)
	}
}

// A multipart upload belongs to its object's tenant: a part presigned with
// another tenant's credential is refused, as the server refuses it.
func TestStrictAuthRefusesAnotherTenantsUpload(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	ctx := context.Background()
	mine := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant())))
	up, err := mine.Data.MultipartUpload.InitiateMultipartUpload(ctx, &datav1.InitiateMultipartUploadRequest{
		Parent: srv.Collection().String(), Key: "k", SizeBytes: 1, ContentType: testContentType,
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	theirs := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(uuid.NewString())))
	_, err = theirs.Data.MultipartUpload.PresignPart(ctx, &datav1.PresignPartRequest{
		UploadId: up.GetUploadId(), PartNumber: 1, ChecksumValue: emptySHA256,
	})
	wantRefused(t, err, uploadTenantMismatch)
}

// Credentials are checked before FailRPC's failures: a refused call does not
// spend one.
func TestStrictAuthComesBeforeFailRPC(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	srv.FailRPC(paladindatav1connect.ObjectServiceGetObjectProcedure, 1, connect.CodeUnavailable)
	wantRefused(t, getObject(srv.Connect(), obj), missingAuthorization)
	p := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant())))
	if err := getObject(p, obj); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("err = %v, want the failure still pending", err)
	}
}

// The tokens the fake issues are distinct, and an API token carries the
// server's prefix, so a client tells it from a bearer token.
func TestIssuedTokens(t *testing.T) {
	srv := paladintest.New(t)
	seen := map[string]bool{}
	for _, token := range []string{
		srv.IssueBearerToken(srv.Tenant()), srv.IssueBearerToken(srv.Tenant()),
		srv.IssueAPIToken(srv.Tenant()), srv.IssueCapability(srv.Tenant()),
	} {
		if seen[token] {
			t.Errorf("token %q issued twice", token)
		}
		seen[token] = true
	}
	if token := srv.IssueAPIToken(srv.Tenant()); !strings.HasPrefix(token, "paladin_pat_") {
		t.Errorf("API token %q lacks the server's paladin_pat_ prefix", token)
	}
}
