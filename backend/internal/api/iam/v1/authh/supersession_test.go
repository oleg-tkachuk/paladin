package authh

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
)

// A browser cannot update its cookie between two requests already in flight,
// so the token an ExchangeAudience carries is routinely the one its sibling
// request just traded in. The server used to have no way to see that: `revoked`
// was one boolean covering both "rotated a moment ago by its own holder" and
// "killed for cause", so tolerating the first would have meant tolerating the
// second.
//
// superseded_at separates them, and this is the behaviour that separation buys
// — plus the reason it is safe. The console's BFF kept this same knowledge in
// process memory to paper over the race, which is the single thing preventing
// it from running in more than one replica.

func supersededToken(userID, tenantID uuid.UUID, ago time.Duration) authstore.RefreshToken {
	at := time.Now().Add(-ago)
	return authstore.RefreshToken{
		JTI: uuid.New(), UserID: userID, TenantID: tenantID,
		ExpiresAt: time.Now().Add(time.Hour), Revoked: true, SupersededAt: &at,
	}
}

func exchangeHandler(t *testing.T, refresh *fakeRefresh, userID, tenantID uuid.UUID) *Handler {
	t.Helper()
	users := &fakeUsers{user: authstore.User{
		UserID: userID, TenantID: tenantID, Subject: "u1",
	}}
	return NewHandler(users, refresh, &stubMinter{},
		stubDecoder{jti: uuid.New(), uid: userID, tid: tenantID}, allowAuthorizer{})
}

func TestExchangeAudienceHonoursAJustSupersededToken(t *testing.T) {
	userID, tenantID := uuid.New(), uuid.New()
	refresh := &fakeRefresh{
		getErr: authstore.ErrTokenRevoked,
		anyTok: supersededToken(userID, tenantID, 2*time.Second),
	}
	h := exchangeHandler(t, refresh, userID, tenantID)

	out, err := h.ExchangeAudience(context.Background(), ExchangeAudienceInput{
		RefreshToken:   "the-token-its-sibling-just-rotated",
		TargetAudience: auth.AudienceData,
	})
	if err != nil {
		t.Fatalf("a token superseded 2s ago was refused: %v — this is the race the BFF's in-memory map exists to hide", err)
	}
	if out.AccessToken == "" {
		t.Error("no access token minted")
	}
	// The chain is untouched: this RPC consumes nothing, and tolerating a
	// superseded token must not turn it into a second rotation.
	if len(refresh.superseded) != 0 {
		t.Errorf("ExchangeAudience superseded %v — it must never rotate", refresh.superseded)
	}
}

func TestExchangeAudienceRefusesAnOldSupersession(t *testing.T) {
	userID, tenantID := uuid.New(), uuid.New()
	refresh := &fakeRefresh{
		getErr: authstore.ErrTokenRevoked,
		anyTok: supersededToken(userID, tenantID, 10*time.Minute),
	}
	h := exchangeHandler(t, refresh, userID, tenantID)

	_, err := h.ExchangeAudience(context.Background(), ExchangeAudienceInput{
		RefreshToken: "long-since-rotated", TargetAudience: auth.AudienceData,
	})
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated — the window is for a race, not a second lifetime", code(err))
	}
}

// The half that makes the tolerance safe. A token revoked at logout, or by
// reuse detection killing the family, carries no superseded_at — so it is
// refused here exactly as before. Without this the grace window would keep
// revoked-for-cause tokens working for thirty seconds after the revocation
// meant to stop them.
func TestExchangeAudienceRefusesATokenRevokedForCause(t *testing.T) {
	userID, tenantID := uuid.New(), uuid.New()
	revokedForCause := authstore.RefreshToken{
		JTI: uuid.New(), UserID: userID, TenantID: tenantID,
		ExpiresAt: time.Now().Add(time.Hour), Revoked: true, // SupersededAt nil
	}
	refresh := &fakeRefresh{getErr: authstore.ErrTokenRevoked, anyTok: revokedForCause}
	h := exchangeHandler(t, refresh, userID, tenantID)

	_, err := h.ExchangeAudience(context.Background(), ExchangeAudienceInput{
		RefreshToken: "revoked-at-logout", TargetAudience: auth.AudienceData,
	})
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated — a logout must not be undone by the rotation window", code(err))
	}
}

func TestExchangeAudienceRefusesAnExpiredSupersededToken(t *testing.T) {
	userID, tenantID := uuid.New(), uuid.New()
	tok := supersededToken(userID, tenantID, time.Second)
	tok.ExpiresAt = time.Now().Add(-time.Minute)
	refresh := &fakeRefresh{getErr: authstore.ErrTokenRevoked, anyTok: tok}
	h := exchangeHandler(t, refresh, userID, tenantID)

	_, err := h.ExchangeAudience(context.Background(), ExchangeAudienceInput{
		RefreshToken: "expired", TargetAudience: auth.AudienceData,
	})
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated — expiry outranks the window", code(err))
	}
}

// Rotation records supersession rather than a bare revoke; without that the
// column stays NULL and the whole mechanism is inert.
func TestRefreshTokenRecordsSupersession(t *testing.T) {
	userID, tenantID := uuid.New(), uuid.New()
	jti := uuid.New()
	refresh := &fakeRefresh{getTok: authstore.RefreshToken{
		JTI: jti, UserID: userID, TenantID: tenantID,
		ExpiresAt: time.Now().Add(time.Hour),
	}}
	users := &fakeUsers{user: authstore.User{UserID: userID, TenantID: tenantID, Subject: "u1"}}
	h := NewHandler(users, refresh, &stubMinter{},
		stubDecoder{jti: jti, uid: userID, tid: tenantID}, allowAuthorizer{})

	if _, err := h.RefreshToken(context.Background(), RefreshInput{RefreshToken: "rt"}); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if len(refresh.superseded) != 1 || refresh.superseded[0] != jti {
		t.Fatalf("superseded = %v, want the rotated jti %v — a bare Revoke leaves superseded_at NULL and the grace window never opens", refresh.superseded, jti)
	}
}
