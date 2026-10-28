package mcp

import (
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes/exchange"
	"github.com/oleg-tkachuk/limes/exchange/exchangetest"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
)

// The IAM issuer the subject tokens below come from.
const (
	subjectIssuer     = "paladin"
	subjectKey        = "subject-test-signing-key-32-bytes-long"
	subjectOtherKey   = "another-signing-key-of-32-bytes-long!!"
	subjectClientID   = "agent"
	subjectUser       = "user-1"
	subjectTokenTTL   = time.Minute
	subjectExpiredAgo = time.Hour
)

func subjectMinter(t *testing.T, name, key string, now time.Time) *issuer.Issuer {
	t.Helper()
	iss, err := issuer.New(issuer.Config{
		Issuer: name, SigningKey: []byte(key), Now: func() time.Time { return now },
		AccessTokenTTL: subjectTokenTTL, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

func mintSubject(t *testing.T, iss *issuer.Issuer, client string) (string, time.Time) {
	t.Helper()
	tok, exp, err := iss.MintAccess(issuer.AccessClaims{
		Subject: subjectUser, TenantID: uuid.New(), Audience: auth.AudienceMCP,
		OAuthClientID: client, OAuthScopes: []string{auth.OAuthScopeRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok, exp
}

// A caller's paladin-mcp token is read for limes' exchange as the IAM
// issuer wrote it, and anything else is refused as invalid_grant.
func TestSubjectVerifierKeepsTheContract(t *testing.T) {
	v := NewSubjectVerifier(&auth.JWTVerifier{
		Key: []byte(subjectKey), ExpectedIssuer: subjectIssuer, ExpectedAudience: auth.AudienceMCP,
	}, subjectIssuer)
	now := time.Now()

	exchangetest.TestSubjectVerifier(t, v, exchangetest.Tokens{
		Valid: func(t *testing.T) (string, exchange.Subject) {
			tok, exp := mintSubject(t, subjectMinter(t, subjectIssuer, subjectKey, now), subjectClientID)
			return tok, exchange.Subject{
				Issuer: subjectIssuer, Subject: subjectUser, ClientID: subjectClientID,
				Audience: []string{auth.AudienceMCP}, ExpiresAt: exp.Truncate(time.Second),
			}
		},
		Expired: func(t *testing.T) string {
			tok, _ := mintSubject(t, subjectMinter(t, subjectIssuer, subjectKey, now.Add(-subjectExpiredAgo)), subjectClientID)
			return tok
		},
		OtherIssuer: func(t *testing.T) string {
			tok, _ := mintSubject(t, subjectMinter(t, "elsewhere", subjectKey, now), subjectClientID)
			return tok
		},
		Forged: func(t *testing.T) string {
			tok, _ := mintSubject(t, subjectMinter(t, subjectIssuer, subjectOtherKey, now), subjectClientID)
			return tok
		},
		NoClient: func(t *testing.T) string {
			tok, _ := mintSubject(t, subjectMinter(t, subjectIssuer, subjectKey, now), "")
			return tok
		},
	})
}

// The scopes the user consented to reach the exchange policy.
func TestSubjectVerifierReadsTheConsent(t *testing.T) {
	v := NewSubjectVerifier(&auth.JWTVerifier{
		Key: []byte(subjectKey), ExpectedIssuer: subjectIssuer, ExpectedAudience: auth.AudienceMCP,
	}, subjectIssuer)
	tok, _ := mintSubject(t, subjectMinter(t, subjectIssuer, subjectKey, time.Now()), subjectClientID)
	got, err := v.VerifySubject(t.Context(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != auth.OAuthScopeRead {
		t.Errorf("scopes = %v, want [%s]", got.Scopes, auth.OAuthScopeRead)
	}
}
