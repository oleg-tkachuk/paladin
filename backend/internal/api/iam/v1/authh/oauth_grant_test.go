package authh

import (
	"context"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// A refresh token the OAuth Authorization Server issued names one client, one
// audience and the scopes a user consented to. The console's RPCs mint any
// audience the user may hold, so one presented there would turn an agent's
// paladin-data grant into paladin-admin. They refuse it (ADR-0035).

func oauthIssuedToken(userID, tenantID uuid.UUID) authstore.RefreshToken {
	return authstore.RefreshToken{
		JTI: uuid.New(), FamilyID: uuid.New(), UserID: userID, TenantID: tenantID,
		ExpiresAt: time.Now().Add(time.Hour),
		OAuth:     &authstore.OAuthGrant{ClientID: "desktop-agent", Audience: auth.AudienceData},
	}
}

func TestConsoleRefusesAnOAuthIssuedToken(t *testing.T) {
	userID, tenantID := uuid.New(), uuid.New()
	live := func() *fakeRefresh { return &fakeRefresh{getTok: oauthIssuedToken(userID, tenantID)} }
	superseded := func() *fakeRefresh {
		tok := oauthIssuedToken(userID, tenantID)
		at := time.Now().Add(-time.Second)
		tok.Revoked, tok.SupersededAt = true, &at
		return &fakeRefresh{getErr: authstore.ErrTokenRevoked, anyTok: tok}
	}

	for _, tc := range []struct {
		name    string
		refresh func() *fakeRefresh
		call    func(*Handler) error
	}{
		{"RefreshToken, live token", live, func(h *Handler) error {
			_, err := h.RefreshToken(context.Background(), RefreshInput{RefreshToken: "t", RequestedAudience: auth.AudienceAdmin})
			return err
		}},
		{"RefreshToken, just-rotated token", superseded, func(h *Handler) error {
			_, err := h.RefreshToken(context.Background(), RefreshInput{RefreshToken: "t", RequestedAudience: auth.AudienceAdmin})
			return err
		}},
		{"ExchangeAudience, live token", live, func(h *Handler) error {
			_, err := h.ExchangeAudience(context.Background(), ExchangeAudienceInput{RefreshToken: "t", TargetAudience: auth.AudienceAdmin})
			return err
		}},
		{"ExchangeAudience, just-rotated token", superseded, func(h *Handler) error {
			_, err := h.ExchangeAudience(context.Background(), ExchangeAudienceInput{RefreshToken: "t", TargetAudience: auth.AudienceAdmin})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refresh := tc.refresh()
			h := exchangeHandler(t, refresh, userID, tenantID)
			if err := tc.call(h); connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("err = %v, want Unauthenticated", err)
			}
			if refresh.inserts != 0 || len(refresh.superseded) != 0 {
				t.Errorf("a refused token must rotate nothing: inserts=%d superseded=%v", refresh.inserts, refresh.superseded)
			}
		})
	}
}
