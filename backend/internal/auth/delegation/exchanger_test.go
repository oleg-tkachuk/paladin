package delegation

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"
	"github.com/oleg-tkachuk/limes/exchange/exchangetest"
	"github.com/oleg-tkachuk/limes/memstore"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// exchangerIssuerName is the delegation issuer the exchanger below issues
// through.
const exchangerIssuerName = "paladin-capability/mcp"

// The exchange of ADR-0041 hands each paladin-mcp token one capability:
// the token exchanged again, or by replicas racing, gets the same one back,
// a revoked one is not issued anew, and a token without a jti is refused.
func TestExchangerIssuesOneCapabilityPerToken(t *testing.T) {
	store, err := memstore.NewStore(memstore.StoreConfig{})
	if err != nil {
		t.Fatal(err)
	}
	key, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		t.Fatal(err)
	}
	capIssuer, err := limes.NewIssuer(limes.IssuerConfig{Signer: signer, Store: store, IssuerName: exchangerIssuerName})
	if err != nil {
		t.Fatal(err)
	}
	user := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
	subjects := NewSubjectVerifier(&auth.JWTVerifier{
		Key: []byte(subjectKey), ExpectedIssuer: subjectIssuer, ExpectedAudience: auth.AudienceMCP,
	}, subjectIssuer)
	ex, err := NewExchanger(capIssuer, subjects, store, usersByID{users: map[uuid.UUID]authstore.User{user.UserID: user}},
		limes.AbstractUnitCode, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	minter := subjectMinter(t, subjectIssuer, subjectKey, time.Now())
	request := func(token string) exchange.Request {
		return exchange.Request{
			Client:       exchange.Client{ID: relayClientID, SubjectAudiences: []string{auth.AudienceMCP}},
			SubjectToken: token,
			Audiences:    []string{auth.TokenPlaneData},
		}
	}

	exchangetest.TestOnePerToken(t, exchangetest.OnePerToken{
		Exchanger: ex,
		Request: func(t *testing.T) exchange.Request {
			t.Helper()
			tok, _, err := minter.MintAccess(userAccess(user))
			if err != nil {
				t.Fatal(err)
			}
			return request(tok)
		},
		NoJTI: func(t *testing.T) exchange.Request {
			t.Helper()
			return request(withoutJTI(t, user))
		},
		Revoke: func(t *testing.T, id uuid.UUID) {
			t.Helper()
			if _, err := store.Revoke(t.Context(), limes.RevokeRequest{CapabilityID: id}); err != nil {
				t.Fatal(err)
			}
		},
	})
}

// withoutJTI is a paladin-mcp token for user that the subject verifier
// accepts in every claim but names no jti, which the IAM issuer always
// writes.
func withoutJTI(t *testing.T, user authstore.User) string {
	t.Helper()
	now := time.Now()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(map[string]string{"alg": "HS256", "typ": "JWT"}) + "." + enc(map[string]any{
		"iss": subjectIssuer, "sub": user.UserID.String(), "aud": auth.AudienceMCP,
		"nbf": now.Unix(), "exp": now.Add(subjectTokenTTL).Unix(),
		"tenant": user.TenantID.String(), "client_id": subjectClientID, "scope": auth.OAuthScopeRead,
	})
	mac := hmac.New(sha256.New, []byte(subjectKey))
	mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// userAccess is the claims of a paladin-mcp token the agent holds for user.
func userAccess(user authstore.User) issuer.AccessClaims {
	return issuer.AccessClaims{
		Subject: user.UserID.String(), TenantID: user.TenantID, Audience: auth.AudienceMCP,
		OAuthClientID: subjectClientID, OAuthScopes: []string{auth.OAuthScopeRead},
	}
}
