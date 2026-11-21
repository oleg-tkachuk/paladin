package auth

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
)

// apiTokenPackage is the package name an API token refusal used to begin
// with: the implementation's, nothing the caller can act on.
const apiTokenPackage = "api_token"

// What a caller is told when its API token is refused, wraps included.
const (
	msgTokenMalformed    = "malformed API token"
	msgTokenNotFound     = "API token not found"
	msgTokenExpired      = "API token expired"
	msgTokenRevoked      = "API token revoked"
	msgAudienceMismatch  = `API token audience mismatch: [admin] lacks "data"`
	msgRateLimitZero     = "API token rate limit is 0 rpm; the token admits no request"
	audienceAdmin        = "admin"
	audienceData         = "data"
	mcpAudienceMatchText = "audience mismatch"
)

// The refusals the interceptor sends name the token and the reason, not the
// package that verified it.
func TestAPITokenRefusalsNameNoPackage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		code connect.Code
		want string
	}{
		{api_token.ErrTokenMalformed, connect.CodeUnauthenticated, msgTokenMalformed},
		{api_token.ErrTokenNotFound, connect.CodeUnauthenticated, msgTokenNotFound},
		{api_token.ErrTokenExpired, connect.CodeUnauthenticated, msgTokenExpired},
		{api_token.ErrTokenRevoked, connect.CodeUnauthenticated, msgTokenRevoked},
		// The verifier's own wrap, as Verify returns it.
		{fmt.Errorf("%w: %v lacks %q", api_token.ErrAudienceMismatch, []string{audienceAdmin}, audienceData), connect.CodePermissionDenied, msgAudienceMismatch},
	} {
		got := mapAPITokenErr(tc.err)
		if connect.CodeOf(got) != tc.code {
			t.Errorf("%v: code %v, want %v", tc.err, connect.CodeOf(got), tc.code)
		}
		assertRefusalText(t, got, tc.want)
	}
}

// The MCP bridge tells a token for the wrong plane by this phrase in the
// message, so rewording the refusal must keep it.
func TestAPITokenAudienceRefusalKeepsTheBridgePhrase(t *testing.T) {
	t.Parallel()
	if !strings.Contains(api_token.ErrAudienceMismatch.Error(), mcpAudienceMatchText) {
		t.Errorf("%q no longer says %q", api_token.ErrAudienceMismatch, mcpAudienceMatchText)
	}
}

func TestRateLimitZeroRefusalNamesNoPackage(t *testing.T) {
	t.Parallel()
	i := &apiTokenInterceptor{audience: audienceData}
	err := i.rateLimitGate(context.Background(), &api_token.Token{ID: uuid.New(), RateLimitRPM: new(0)})
	assertRefusalText(t, err, msgRateLimitZero)
}

// assertRefusalText fails unless err is a *connect.Error whose message is
// exactly want, with no package name in it.
func assertRefusalText(t *testing.T, err error, want string) {
	t.Helper()
	cerr, ok := err.(*connect.Error) //nolint:errorlint // the interceptor returns it unwrapped
	if !ok {
		t.Fatalf("%v is not a *connect.Error", err)
	}
	if cerr.Message() != want {
		t.Errorf("message %q, want %q", cerr.Message(), want)
	}
	if strings.Contains(cerr.Message(), apiTokenPackage) {
		t.Errorf("message %q names the package", cerr.Message())
	}
}
