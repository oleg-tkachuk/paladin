package authh

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
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

// revokedFamilyToken is a member of a family that was deliberately killed.
// RevokeRefreshTokenFamily revokes AND clears superseded_at across the family,
// so a predecessor of the dead head no longer reads as "rotated a moment ago"
// — which is exactly what used to keep it usable for another thirty seconds.
func revokedFamilyToken(userID, tenantID uuid.UUID) authstore.RefreshToken {
	return authstore.RefreshToken{
		JTI: uuid.New(), FamilyID: uuid.New(), UserID: userID, TenantID: tenantID,
		ExpiresAt: time.Now().Add(time.Hour), Revoked: true, SupersededAt: nil,
	}
}

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

// ─── the rotation race ──────────────────────────────────────────────────────
//
// Two tabs opened together each run the console's session bootstrap, and both
// carry the same cookie — the browser cannot update it between two requests
// already in flight. Both therefore present the same live refresh token to
// RefreshToken, both read it as valid (the reads cannot see each other), and
// one of them is about to lose.
//
// The BFF used to hold an in-process map so the loser could join the winner's
// result. That map is why the console was pinned to a single replica: two
// replicas do not share it, and the loser routed to the wrong one was signed
// out of a working session. The verdict belongs in the database, which is the
// one thing every replica already shares.

func TestRefreshTokenLoserOfARotationRaceIsToldToExchange(t *testing.T) {
	userID, tenantID := uuid.New(), uuid.New()
	refresh := &fakeRefresh{
		getTok: authstore.RefreshToken{
			JTI: uuid.New(), FamilyID: uuid.New(), UserID: userID, TenantID: tenantID,
			ExpiresAt: time.Now().Add(time.Hour),
		},
		// The read succeeded — then the conditional UPDATE matched nothing,
		// because a sibling superseded the row in between.
		supersedeErr: authstore.ErrAlreadyRotated,
	}
	h := exchangeHandler(t, refresh, userID, tenantID)

	_, err := h.RefreshToken(context.Background(), RefreshInput{RefreshToken: "rt"})
	if err == nil {
		t.Fatal("a lost race was treated as a successful rotation")
	}
	// Aborted, not Unauthenticated: the session is valid and the caller can
	// retry differently. Unauthenticated here is what signed operators out.
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
	if refresh.revokeFamilies != 0 {
		t.Error("revoked the family over a lost race — the loser holds a token its own sibling traded in, not a stolen one")
	}
	if refresh.inserts != 0 {
		t.Error("minted a successor after losing the race — the family would have forked into two live chains")
	}
}

func TestRefreshTokenReplayOfAJustSupersededTokenIsARaceNotTheft(t *testing.T) {
	// Same race, arriving a moment later: the sibling's UPDATE has already
	// landed, so the read itself fails. The answer must be identical — this
	// is timing, not a different event.
	userID, tenantID := uuid.New(), uuid.New()
	refresh := &fakeRefresh{
		getErr: authstore.ErrTokenRevoked,
		anyTok: supersededToken(userID, tenantID, 2*time.Second),
	}
	h := exchangeHandler(t, refresh, userID, tenantID)

	_, err := h.RefreshToken(context.Background(), RefreshInput{RefreshToken: "rt"})
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
	if refresh.revokeFamilies != 0 {
		t.Error("revoked the family for a token superseded seconds ago")
	}
}

func TestRefreshTokenStillRevokesTheFamilyOnAGenuineReplay(t *testing.T) {
	// The property the race handling must not cost: a token revoked for cause
	// — logout, or an earlier reuse detection — has no superseded_at, and a
	// token superseded long ago is outside the window. Both are theft signals
	// and both must still kill the family.
	userID, tenantID := uuid.New(), uuid.New()
	base := authstore.RefreshToken{
		JTI: uuid.New(), FamilyID: uuid.New(), UserID: userID, TenantID: tenantID,
		ExpiresAt: time.Now().Add(time.Hour), Revoked: true,
	}
	long := time.Now().Add(-10 * time.Minute)

	for _, tc := range []struct {
		name string
		tok  authstore.RefreshToken
	}{
		{"revoked for cause, never superseded", base},
		{"superseded far outside the grace window", func() authstore.RefreshToken {
			t := base
			t.SupersededAt = &long
			return t
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refresh := &fakeRefresh{getErr: authstore.ErrTokenRevoked, anyTok: tc.tok}
			h := exchangeHandler(t, refresh, userID, tenantID)

			_, err := h.RefreshToken(context.Background(), RefreshInput{RefreshToken: "rt"})
			if code(err) != connect.CodeUnauthenticated {
				t.Fatalf("code = %v, want Unauthenticated", code(err))
			}
			if refresh.revokeFamilies != 1 {
				t.Errorf("family revocations = %d, want 1 — reuse detection went quiet", refresh.revokeFamilies)
			}
		})
	}
}

func TestRefreshTokenWinnerOfTheRaceRotatesNormally(t *testing.T) {
	userID, tenantID := uuid.New(), uuid.New()
	refresh := &fakeRefresh{getTok: authstore.RefreshToken{
		JTI: uuid.New(), FamilyID: uuid.New(), UserID: userID, TenantID: tenantID,
		ExpiresAt: time.Now().Add(time.Hour),
	}}
	h := exchangeHandler(t, refresh, userID, tenantID)

	out, err := h.RefreshToken(context.Background(), RefreshInput{RefreshToken: "rt"})
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if out.RefreshToken == "" {
		t.Error("winner got no successor")
	}
	if len(refresh.superseded) != 1 {
		t.Errorf("supersedes = %d, want 1", len(refresh.superseded))
	}
	if refresh.revokeFamilies != 0 {
		t.Error("revoked the family on a normal rotation")
	}
}

// ─── the window must close when the session ends ────────────────────────────
//
// The grace window reads superseded_at, which is stamped once and never
// revisited. So a token stayed "recently superseded" even after the family it
// belonged to was deliberately killed, and the window kept honouring it for
// the rest of its thirty seconds. Two ways that mattered, both real:
//
//   - after a logout, the PREDECESSOR of the revoked head still minted access
//     tokens. Reproduced against a live stack: log in, load a page (which
//     rotates), log out, present the pre-rotation token — 200.
//   - after reuse detection revoked a family on suspicion of theft, every
//     superseded member of it stayed usable for the same half minute. Worse,
//     because that revocation exists precisely to stop an attacker.
//
// The family's own state is what closes it: a rotation leaves exactly one live
// head, a logout or a family revoke leaves none.

func TestExchangeAudienceRefusesASupersededTokenOnceTheFamilyIsDead(t *testing.T) {
	userID, tenantID := uuid.New(), uuid.New()
	refresh := &fakeRefresh{
		getErr: authstore.ErrTokenRevoked,
		// Superseded two seconds ago — well inside the window — but the
		// session it belonged to has been ended.
		// A killed family, as the database leaves it: revoked, and
		// supersession CLEARED, so nothing here reads as a mere rotation.
		anyTok: revokedFamilyToken(userID, tenantID),
	}
	h := exchangeHandler(t, refresh, userID, tenantID)

	_, err := h.ExchangeAudience(context.Background(), ExchangeAudienceInput{
		RefreshToken:   "the-token-the-logout-was-supposed-to-kill",
		TargetAudience: auth.AudienceData,
	})
	if err == nil {
		t.Fatal("a logged-out session still minted an access token")
	}
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", code(err))
	}
}

func TestRefreshTokenRefusesASupersededTokenOnceTheFamilyIsDead(t *testing.T) {
	// Same rule on the rotating path: without it, a logged-out session could
	// be resumed by presenting the predecessor of its revoked head, and the
	// answer would be the friendly Aborted meant for a lost race.
	userID, tenantID := uuid.New(), uuid.New()
	refresh := &fakeRefresh{
		getErr: authstore.ErrTokenRevoked,
		// A killed family, as the database leaves it: revoked, and
		// supersession CLEARED, so nothing here reads as a mere rotation.
		anyTok: revokedFamilyToken(userID, tenantID),
	}
	h := exchangeHandler(t, refresh, userID, tenantID)

	_, err := h.RefreshToken(context.Background(), RefreshInput{RefreshToken: "rt"})
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", code(err))
	}
	if refresh.revokeFamilies != 1 {
		t.Errorf("family revocations = %d, want 1 — a token presented after its family died is a theft signal", refresh.revokeFamilies)
	}
}

func TestSupersessionGraceStillHoldsWhileTheFamilyLives(t *testing.T) {
	// The property the fix must not cost: an ordinary rotation leaves a live
	// head, and its predecessor is still honoured inside the window. That is
	// the two-tab case the whole mechanism exists for.
	userID, tenantID := uuid.New(), uuid.New()
	refresh := &fakeRefresh{
		getErr: authstore.ErrTokenRevoked,
		anyTok: supersededToken(userID, tenantID, 2*time.Second),
	}
	h := exchangeHandler(t, refresh, userID, tenantID)

	out, err := h.ExchangeAudience(context.Background(), ExchangeAudienceInput{
		RefreshToken:   "rotated-by-a-sibling-a-moment-ago",
		TargetAudience: auth.AudienceData,
	})
	if err != nil {
		t.Fatalf("the live-family case regressed: %v", err)
	}
	if out.AccessToken == "" {
		t.Error("no access token minted")
	}
}
