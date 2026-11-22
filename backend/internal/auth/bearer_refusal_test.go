package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
)

// The JWT library's own words, which a refusal must never carry to the caller.
var libraryText = []string{"jws.", "jwt.", "jwk.", "jwt:", "jws:"}

// mcpAudiencePhrase is what the MCP bridge looks for in a refusal to tell a
// token issued for another plane (internal/mcp audienceMismatch).
const mcpAudiencePhrase = "audience mismatch"

// notAToken is a bearer no parser reads as a JWT, as a client sent it live.
const notAToken = "not-a-token"

// What the test verifier expects of the tokens it is shown.
const (
	refusalIssuer   = "paladin-test"
	refusalAudience = "paladin-data"
	otherAudience   = "paladin-admin"
	otherIssuer     = "someone-else"
)

// refusalClock is the verifier's now, fixed so expiry is deterministic.
var refusalClock = time.Unix(1_700_000_000, 0)

// Every message a refused bearer token can be answered with names the token;
// that none names a package the apiutil sentinel guard checks.
func TestBearerRefusalsNameTheCredential(t *testing.T) {
	t.Parallel()
	for _, msg := range BearerRefusals() {
		// RefreshRefusal renames the credential in this phrase, so every
		// reason must carry it.
		if !strings.Contains(msg, bearerCredentialName) {
			t.Errorf("%q does not name the credential", msg)
		}
	}
}

// The MCP bridge tells a token for the wrong plane by this phrase, so the
// audience refusal must keep it.
func TestBearerAudienceRefusalKeepsTheBridgePhrase(t *testing.T) {
	t.Parallel()
	if !strings.Contains(ErrBearerAudienceMismatch.Error(), mcpAudiencePhrase) {
		t.Errorf("%q no longer says %q", ErrBearerAudienceMismatch, mcpAudiencePhrase)
	}
}

// refusalVerifier is a key set verifier over a fresh key, and a function that
// signs claims with it.
func refusalVerifier(t *testing.T) (*KeySetVerifier, func(map[string]any) string) {
	t.Helper()
	set, _ := keySet(t)
	v := &KeySetVerifier{
		Keys: set.Public(), ExpectedIssuer: refusalIssuer, ExpectedAudience: refusalAudience,
		Leeway: time.Second, Now: func() time.Time { return refusalClock },
	}
	return v, func(claims map[string]any) string { return signedBySet(t, set, claims) }
}

// claimsWith is baseClaims at refusalClock with key set to value.
func claimsWith(key string, value any) map[string]any {
	c := baseClaims(refusalClock)
	c[key] = value
	return c
}

// A bearer the verifier refuses is answered Unauthenticated with the reason
// alone: the library's text, which quoted its parser to the caller live
// ("jws.ParseString: failed to parse string: ..."), stays in the cause for
// the failure log. Both the planes' gate and the IAM plane's answer alike.
func TestBearerRefusalCarriesNoLibraryText(t *testing.T) {
	t.Parallel()
	v, sign := refusalVerifier(t)
	other, _ := keySet(t)
	for _, tc := range []struct {
		name  string
		token string
		want  error
	}{
		{"not a token", notAToken, ErrBearerMalformed},
		{"expired", sign(claimsWith("exp", refusalClock.Add(-time.Hour).Unix())), ErrBearerExpired},
		{"not yet valid", sign(claimsWith("nbf", refusalClock.Add(time.Hour).Unix())), ErrBearerNotYetValid},
		{"another issuer", sign(claimsWith("iss", otherIssuer)), ErrBearerIssuerMismatch},
		{"another audience", sign(claimsWith("aud", otherAudience)), ErrBearerAudienceMismatch},
		{"another tenant shape", sign(claimsWith("tenant", notAToken)), ErrBearerClaims},
		{"an unknown key", signedBySet(t, other, baseClaims(refusalClock)), ErrBearerUnknownKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, gateErr := principalFromHeaders(context.Background(), v, bearerPrefix+tc.token)
			_, iamErr := NewPermissiveInterceptor(v).attach(context.Background(), bearerPrefix+tc.token, "")
			for _, err := range []error{gateErr, iamErr} {
				assertBearerRefusal(t, err, tc.want)
			}
		})
	}
}

// jwsParserText is how the library names its parser in the error it returns
// for a bearer that is no JWT: what reached the caller live.
const jwsParserText = "jws."

// The parser's words are kept, only not sent: the failure log reads them
// from the cause.
func TestMalformedBearerKeepsTheParserErrorForTheLog(t *testing.T) {
	t.Parallel()
	v, _ := refusalVerifier(t)
	_, err := principalFromHeaders(context.Background(), v, bearerPrefix+notAToken)
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("%v is not a *connect.Error", err)
	}
	if strings.Contains(cerr.Message(), jwsParserText) {
		t.Errorf("message %q carries the parser's text", cerr.Message())
	}
	if !strings.Contains(cerr.Unwrap().Error(), jwsParserText) {
		t.Errorf("cause %q lost the parser's text", cerr.Unwrap())
	}
}

// The IAM plane's RefreshToken and ExchangeAudience answered a refresh token
// the decoder refused with its error text — "refresh token: jwt: malformed
// token: jws.ParseString: …". Each is now the bearer reason, naming the
// refresh token, with the decoder's text kept as the cause.
func TestRefreshRefusalNamesTheRefreshToken(t *testing.T) {
	t.Parallel()
	key := ecKey(t)
	now := time.Unix(1_700_000_000, 0)
	user, tenant, jti := uuid.New(), uuid.New(), uuid.New()
	token := func(edit func(map[string]any)) string { return refreshToken(t, key, now, user, tenant, jti, edit) }
	for _, tc := range []struct {
		name  string
		dec   *RefreshDecoder
		token string
		want  string
	}{
		{"not a token", refreshDecoder(key, now), notAToken, "malformed refresh token"},
		{"expired", refreshDecoder(key, now), token(func(c map[string]any) { c["exp"] = now.Add(-time.Hour).Unix() }), "refresh token expired"},
		{"another audience", refreshDecoder(key, now), token(func(c map[string]any) { c["aud"] = otherAudience }), "refresh token audience mismatch"},
		{"an access token", refreshDecoder(key, now), token(func(c map[string]any) { delete(c, "refresh") }), "refresh token claims invalid"},
		{"no verifier", &RefreshDecoder{}, token(nil), "refresh token could not be verified"},
	} {
		_, _, _, err := tc.dec.DecodeRefresh(tc.token)
		got := RefreshRefusal(err)
		if got.Code() != connect.CodeUnauthenticated || got.Message() != tc.want {
			t.Errorf("%s: %v %q, want %v %q", tc.name, got.Code(), got.Message(), connect.CodeUnauthenticated, tc.want)
		}
		for _, lib := range libraryText {
			if strings.Contains(got.Message(), lib) {
				t.Errorf("%s: message %q carries the library's %q", tc.name, got.Message(), lib)
			}
		}
		if !errors.Is(got, err) {
			t.Errorf("%s: the decoder's error is not the cause", tc.name)
		}
	}
}

// A failure that is none of the reasons — a key set that would not load — is
// answered with a fixed sentence, its cause kept for the log.
func TestUnclassifiedBearerFailureIsAFixedSentence(t *testing.T) {
	t.Parallel()
	cause := errors.New("jwks: refresh on miss: Get \"https://idp.internal/jwks\": dial tcp: refused")
	got := bearerRefusal(cause)
	if got.Message() != bearerUnverified {
		t.Errorf("message %q, want %q", got.Message(), bearerUnverified)
	}
	if !errors.Is(got, cause) {
		t.Error("the failure is not the refusal's cause")
	}
}

// assertBearerRefusal fails unless err is an Unauthenticated *connect.Error
// whose message is exactly want's text, free of the library's, and whose cause
// still carries want and the library's words.
func assertBearerRefusal(t *testing.T, err, want error) {
	t.Helper()
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("%v is not a *connect.Error", err)
	}
	if cerr.Code() != connect.CodeUnauthenticated {
		t.Errorf("code %v, want %v", cerr.Code(), connect.CodeUnauthenticated)
	}
	if cerr.Message() != want.Error() {
		t.Errorf("message %q, want %q", cerr.Message(), want)
	}
	for _, lib := range libraryText {
		if strings.Contains(cerr.Message(), lib) {
			t.Errorf("message %q carries the library's %q", cerr.Message(), lib)
		}
	}
	if !errors.Is(cerr, want) {
		t.Errorf("cause %v does not wrap %v", cerr.Unwrap(), want)
	}
	if cerr.Unwrap().Error() == cerr.Message() {
		t.Errorf("cause %q adds nothing for the log to the message", cerr.Unwrap())
	}
}
