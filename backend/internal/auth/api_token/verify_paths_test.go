package api_token

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// touchFailingStore verifies like memStore but cannot stamp last_used_at.
type touchFailingStore struct{ *memStore }

func (touchFailingStore) TouchLastUsed(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	return errors.New("connection reset")
}

// A last_used_at stamp that fails never fails the verify — a bookkeeping
// write must not lock anyone out — but says so: the column is how a stale
// token is found and revoked.
func TestVerifySurvivesAFailedLastUsedStamp(t *testing.T) {
	store := touchFailingStore{newMemStore()}
	hasher := mustHasher(t)
	issuer, err := NewIssuer(IssuerConfig{Store: store, Hasher: hasher, MaxTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(VerifierConfig{Store: store, Hasher: hasher, TouchLastUsed: true})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := issuer.Issue(context.Background(), IssueRequest{TenantID: uuid.New(), Name: "svc", Audience: []string{"data"}})
	if err != nil {
		t.Fatal(err)
	}
	core, logs := observer.New(zapcore.WarnLevel)
	ctx := logger.WithContext(context.Background(), zap.New(core))
	if _, err := verifier.Verify(ctx, tok.Plaintext, "data"); err != nil {
		t.Fatalf("Verify = %v, want the token accepted", err)
	}
	if logs.Len() != 1 || logs.All()[0].ContextMap()["token_id"] != tok.ID.String() {
		t.Fatalf("want one warning naming the token, got %v", logs.All())
	}
}

func TestIssuerDefaultsItsMaxTTL(t *testing.T) {
	issuer, err := NewIssuer(IssuerConfig{Store: newMemStore(), Hasher: mustHasher(t)})
	if err != nil {
		t.Fatal(err)
	}
	if issuer.MaxTTL != DefaultMaxTTL {
		t.Fatalf("MaxTTL = %v, want %v", issuer.MaxTTL, DefaultMaxTTL)
	}
}

func TestTokenIsActive(t *testing.T) {
	now := time.Unix(1_900_000_000, 0)
	revokedAt := now.Add(-time.Minute)
	for _, tc := range []struct {
		name string
		tok  Token
		want bool
	}{
		{"live", Token{ExpiresAt: now.Add(time.Hour)}, true},
		{"expires now", Token{ExpiresAt: now}, false},
		{"expired", Token{ExpiresAt: now.Add(-time.Hour)}, false},
		{"revoked", Token{ExpiresAt: now.Add(time.Hour), RevokedAt: &revokedAt}, false},
	} {
		if got := tc.tok.IsActive(now); got != tc.want {
			t.Errorf("%s: IsActive = %v, want %v", tc.name, got, tc.want)
		}
	}
}
