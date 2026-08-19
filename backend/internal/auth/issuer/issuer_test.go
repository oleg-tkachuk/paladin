package issuer

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/internal/auth"
)

func newTestIssuer(t *testing.T) *Issuer {
	t.Helper()
	iss, err := New(Config{
		Issuer:            "paladin-test",
		SigningKey:        []byte("test-signing-key-32-bytes-min--padding"),
		AccessTokenTTL:    15 * time.Minute,
		RefreshTokenTTL:   7 * 24 * time.Hour,
		ScopedTokenMaxTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

func TestMintAndVerifyAccess(t *testing.T) {
	iss := newTestIssuer(t)
	tenantID := uuid.Must(uuid.NewV7())
	subject := uuid.Must(uuid.NewV7()).String()

	tok, exp, err := iss.MintAccess(AccessClaims{
		Subject:  subject,
		TenantID: tenantID,
		Audience: auth.AudienceData,
		Roles:    []string{"tenant.user"},
		Scopes:   []auth.Scope{{Type: auth.ScopeBucket, Value: "paladin-archive"}},
		Kind:     auth.PrincipalKindUser,
	})
	if err != nil {
		t.Fatalf("MintAccess: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	if !exp.After(time.Now()) {
		t.Fatalf("expiry not in future: %v", exp)
	}

	v := &auth.JWTVerifier{
		Key:              []byte("test-signing-key-32-bytes-min--padding"),
		ExpectedIssuer:   "paladin-test",
		ExpectedAudience: auth.AudienceData,
	}
	p, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if p.Subject != subject {
		t.Errorf("Subject: got %q want %q", p.Subject, subject)
	}
	if p.TenantID != tenantID {
		t.Errorf("TenantID: got %v want %v", p.TenantID, tenantID)
	}
	if p.Audience != auth.AudienceData {
		t.Errorf("Audience: got %q want %q", p.Audience, auth.AudienceData)
	}
	if !p.HasRole("tenant.user") {
		t.Error("missing role tenant.user")
	}
	if len(p.Scopes) != 1 || p.Scopes[0].Type != auth.ScopeBucket || p.Scopes[0].Value != "paladin-archive" {
		t.Errorf("Scopes: got %+v", p.Scopes)
	}
	if p.Kind != auth.PrincipalKindUser {
		t.Errorf("Kind: got %v want User", p.Kind)
	}
}

func TestMintAccessWrongAudience(t *testing.T) {
	iss := newTestIssuer(t)
	tok, _, err := iss.MintAccess(AccessClaims{
		Subject:  "u",
		Audience: auth.AudienceAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	v := &auth.JWTVerifier{
		Key:              []byte("test-signing-key-32-bytes-min--padding"),
		ExpectedAudience: auth.AudienceData, // mismatch
	}
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected audience mismatch error")
	}
}

func TestMintRefreshDecode(t *testing.T) {
	iss := newTestIssuer(t)
	userID := uuid.Must(uuid.NewV7())
	tokenID := uuid.Must(uuid.NewV7())
	tenantID := uuid.Must(uuid.NewV7())

	tok, _, err := iss.MintRefresh(RefreshClaims{
		Subject:  userID.String(),
		TenantID: tenantID,
		UserID:   userID,
		TokenID:  tokenID,
	})
	if err != nil {
		t.Fatal(err)
	}
	dec := &auth.RefreshDecoder{
		Verifier: &auth.JWTVerifier{
			Key:              []byte("test-signing-key-32-bytes-min--padding"),
			ExpectedIssuer:   "paladin-test",
			ExpectedAudience: auth.AudienceIAM,
		},
	}
	jti, gotUserID, gotTenantID, err := dec.DecodeRefresh(tok)
	if err != nil {
		t.Fatalf("DecodeRefresh: %v", err)
	}
	if jti != tokenID {
		t.Errorf("jti: got %v want %v", jti, tokenID)
	}
	if gotUserID != userID {
		t.Errorf("user_id: got %v want %v", gotUserID, userID)
	}
	if gotTenantID != tenantID {
		t.Errorf("tenant_id: got %v want %v", gotTenantID, tenantID)
	}
}

func TestRefreshDecoderRejectsAccessToken(t *testing.T) {
	iss := newTestIssuer(t)
	tok, _, err := iss.MintAccess(AccessClaims{
		Subject:  "u",
		Audience: auth.AudienceData, // not IAM
	})
	if err != nil {
		t.Fatal(err)
	}
	dec := &auth.RefreshDecoder{
		Verifier: &auth.JWTVerifier{
			Key:              []byte("test-signing-key-32-bytes-min--padding"),
			ExpectedIssuer:   "paladin-test",
			ExpectedAudience: auth.AudienceIAM,
		},
	}
	if _, _, _, err := dec.DecodeRefresh(tok); err == nil {
		t.Fatal("expected audience mismatch")
	}
}
