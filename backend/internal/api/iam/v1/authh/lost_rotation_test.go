package authh

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// A rotation whose response is lost. The client keeps the token it presented
// (P) and never sees the successor (S). Before recovery, P's next presentation
// after the 30-second grace was a replay to reuse detection, which revoked the
// family: reproduced on the cluster by dropping one /api/auth/me Set-Cookie.
//
// These run the handler against a store with the real semantics (memRefresh)
// and a clock the test moves, so each scenario is the sequence of
// presentations it names.

const refreshPrefix = "refresh:"

// jtiMinter signs nothing: a refresh token is its jti, so the decoder can
// read back which row a returned token stands for.
type jtiMinter struct{}

func (jtiMinter) MintAccess(issuer.AccessClaims) (string, time.Time, error) {
	return "access", time.Now().Add(time.Hour), nil
}

func (jtiMinter) MintRefresh(c issuer.RefreshClaims) (string, time.Time, error) {
	exp := c.ExpiresAt
	if exp.IsZero() {
		exp = time.Now().Add(24 * time.Hour)
	}
	return refreshPrefix + c.TokenID.String(), exp, nil
}

type jtiDecoder struct{ uid, tid uuid.UUID }

func (d jtiDecoder) DecodeRefresh(tok string) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	jti, err := uuid.Parse(strings.TrimPrefix(tok, refreshPrefix))
	return jti, d.uid, d.tid, err
}

// memRefresh is RefreshTokenRepository with the postgres adapter's semantics.
type memRefresh struct {
	now  func() time.Time
	rows map[uuid.UUID]*authstore.RefreshToken
}

func (m *memRefresh) Insert(_ context.Context, t authstore.RefreshToken) error {
	m.rows[t.JTI] = &t
	return nil
}

func (m *memRefresh) Get(_ context.Context, jti uuid.UUID) (authstore.RefreshToken, error) {
	r, ok := m.rows[jti]
	if !ok {
		return authstore.RefreshToken{}, authstore.ErrNotFound
	}
	if r.Revoked {
		return authstore.RefreshToken{}, authstore.ErrTokenRevoked
	}
	return *r, nil
}

func (m *memRefresh) GetAny(_ context.Context, jti uuid.UUID) (authstore.RefreshToken, error) {
	r, ok := m.rows[jti]
	if !ok {
		return authstore.RefreshToken{}, authstore.ErrNotFound
	}
	return *r, nil
}

func (m *memRefresh) Revoke(_ context.Context, jti uuid.UUID) error {
	if r, ok := m.rows[jti]; ok {
		r.Revoked = true
	}
	return nil
}

func (m *memRefresh) Supersede(_ context.Context, jti uuid.UUID) error {
	r, ok := m.rows[jti]
	if !ok || r.Revoked {
		return authstore.ErrAlreadyRotated
	}
	at := m.now()
	r.Revoked, r.SupersededAt = true, &at
	return nil
}

func (m *memRefresh) RevokeForUser(context.Context, uuid.UUID) (int64, error) { return 0, nil }

func (m *memRefresh) MarkUsed(_ context.Context, jti uuid.UUID) error {
	if r, ok := m.rows[jti]; ok && r.FirstUsedAt == nil {
		at := m.now()
		r.FirstUsedAt = &at
	}
	return nil
}

func (m *memRefresh) UnusedSuccessor(_ context.Context, parent uuid.UUID) (authstore.RefreshToken, error) {
	for _, r := range m.rows {
		if r.ParentID == parent && r.FirstUsedAt == nil && !r.Revoked {
			return *r, nil
		}
	}
	return authstore.RefreshToken{}, authstore.ErrNotFound
}

func (m *memRefresh) RevokeFamilyOf(_ context.Context, jti uuid.UUID) (int64, error) {
	r, ok := m.rows[jti]
	if !ok {
		return 0, nil
	}
	var n int64
	for _, x := range m.rows {
		if x.FamilyID == r.FamilyID {
			x.Revoked, x.SupersededAt = true, nil
			n++
		}
	}
	return n, nil
}

func (m *memRefresh) PurgeExpired(context.Context, time.Time) (int64, error) { return 0, nil }

// session is one login's family and the clock the handler reads.
type session struct {
	t     *testing.T
	h     *Handler
	store *memRefresh
	clock time.Time
	login string
}

func newSession(t *testing.T) *session {
	t.Helper()
	userID, tenantID := uuid.New(), uuid.New()
	s := &session{t: t, clock: time.Now()}
	s.store = &memRefresh{now: func() time.Time { return s.clock }, rows: map[uuid.UUID]*authstore.RefreshToken{}}
	users := &fakeUsers{user: authstore.User{UserID: userID, TenantID: tenantID, Subject: "u1"}}
	s.h = NewHandler(users, s.store, jtiMinter{}, jtiDecoder{uid: userID, tid: tenantID}, allowAuthorizer{})
	s.h.now = func() time.Time { return s.clock }

	jti := uuid.New()
	_ = s.store.Insert(context.Background(), authstore.RefreshToken{
		JTI: jti, FamilyID: uuid.New(), UserID: userID, TenantID: tenantID,
		IssuedAt: s.clock, ExpiresAt: s.clock.Add(24 * time.Hour),
	})
	s.login = refreshPrefix + jti.String()
	return s
}

func (s *session) wait(d time.Duration) { s.clock = s.clock.Add(d) }

func (s *session) rotate(tok string) (string, error) {
	out, err := s.h.RefreshToken(context.Background(), RefreshInput{RefreshToken: tok})
	if err != nil {
		return "", err
	}
	return out.RefreshToken, nil
}

func (s *session) exchange(tok string) error {
	_, err := s.h.ExchangeAudience(context.Background(), ExchangeAudienceInput{
		RefreshToken: tok, TargetAudience: auth.AudienceData,
	})
	return err
}

func (s *session) familyRevoked() bool {
	for _, r := range s.store.rows {
		if !r.Revoked {
			return false
		}
	}
	return true
}

const pastGrace = supersessionGrace + time.Second

func TestALostRotationIsRecoveredAfterTheGrace(t *testing.T) {
	s := newSession(t)
	succ, err := s.rotate(s.login) // the response carrying succ is lost
	if err != nil {
		t.Fatal(err)
	}
	s.wait(pastGrace)

	got, err := s.rotate(s.login)
	if err != nil {
		t.Fatalf("the holder of a lost rotation was refused: %v", err)
	}
	if got != succ {
		t.Fatalf("recovered %q, want the successor it lost (%q)", got, succ)
	}
	if s.familyRevoked() {
		t.Fatal("the family was revoked")
	}
	// The recovered successor works and rotates on normally.
	if _, err := s.rotate(got); err != nil {
		t.Fatalf("the recovered successor did not rotate: %v", err)
	}
}

func TestExchangeHonoursARecoverableTokenPastTheGrace(t *testing.T) {
	s := newSession(t)
	if _, err := s.rotate(s.login); err != nil {
		t.Fatal(err)
	}
	s.wait(pastGrace)
	// Every plane's exchange on the page load that recovers the rotation
	// carries the old token too.
	if err := s.exchange(s.login); err != nil {
		t.Fatalf("exchange refused a recoverable token: %v", err)
	}
}

func TestReuseDetectionStillHolds(t *testing.T) {
	for _, tc := range []struct {
		name string
		// play runs after the login token was rotated once.
		play func(s *session, succ string)
		// replay is the token presented last, after the grace.
		replay func(s *session, succ string) string
	}{
		{
			name:   "the holder used the successor, then the old token is replayed",
			play:   func(s *session, succ string) { must(s.t, s.exchange(succ)) },
			replay: func(s *session, _ string) string { return s.login },
		},
		{
			name:   "the old token comes back after the recovery window",
			play:   func(s *session, _ string) { s.wait(lostRotationRecoveryWindow) },
			replay: func(s *session, _ string) string { return s.login },
		},
		{
			name: "a thief collected the successor; the holder moved on and the thief comes back",
			play: func(s *session, succ string) {
				s.wait(pastGrace)
				if _, err := s.rotate(s.login); err != nil { // thief, holding the old token
					s.t.Fatal(err)
				}
				next, err := s.rotate(succ) // holder rotates on
				must(s.t, err)
				must(s.t, s.exchange(next)) // and uses the new head
			},
			replay: func(_ *session, succ string) string { return succ },
		},
		{
			name: "the session was logged out",
			play: func(s *session, succ string) {
				jti, _, _, _ := jtiDecoder{}.DecodeRefresh(succ)
				_, err := s.store.RevokeFamilyOf(context.Background(), jti)
				must(s.t, err)
			},
			replay: func(s *session, _ string) string { return s.login },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t)
			succ, err := s.rotate(s.login)
			must(t, err)
			tc.play(s, succ)
			s.wait(pastGrace)

			_, err = s.rotate(tc.replay(s, succ))
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("code = %v, want Unauthenticated", connect.CodeOf(err))
			}
			if !s.familyRevoked() {
				t.Fatal("the family survived a replay")
			}
		})
	}
}

func TestARotationRaceInsideTheGraceIsStillARace(t *testing.T) {
	s := newSession(t)
	_, err := s.rotate(s.login)
	must(t, err)
	s.wait(supersessionGrace / 2)
	if _, err := s.rotate(s.login); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted — inside the grace the loser exchanges", connect.CodeOf(err))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
