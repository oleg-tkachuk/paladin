package authh

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// exchangeFixture is a refresh token for a user the exchange would honour,
// which each case then spoils one way.
type exchangeFixture struct {
	users   *fakeUsers
	refresh *fakeRefresh
	decoder stubDecoder
	minter  *stubMinter
	in      ExchangeAudienceInput
}

func newExchangeFixture() *exchangeFixture {
	uid, tid := uuid.New(), uuid.New()
	return &exchangeFixture{
		users: &fakeUsers{user: authstore.User{
			UserID: uid, TenantID: tid, Subject: "u1", Roles: []string{apiutil.RolePlatformAdmin},
		}},
		refresh: &fakeRefresh{getTok: authstore.RefreshToken{
			UserID: uid, TenantID: tid, ExpiresAt: time.Now().Add(time.Hour),
		}},
		decoder: stubDecoder{jti: uuid.New(), uid: uid, tid: tid},
		minter:  &stubMinter{},
		in:      ExchangeAudienceInput{RefreshToken: "refresh", TargetAudience: auth.AudienceAdmin},
	}
}

func (f *exchangeFixture) run() (*ExchangeAudienceOutput, error) {
	h := NewHandler(f.users, f.refresh, f.minter, f.decoder, allowAuthorizer{})
	return h.ExchangeAudience(context.Background(), f.in)
}

// The fixture itself is exchanged: every refusal below is the one change it
// makes, not the fixture being wrong.
func TestExchangeAudience_HonoursAValidToken(t *testing.T) {
	f := newExchangeFixture()
	out, err := f.run()
	if err != nil || out.AccessToken == "" {
		t.Fatalf("ExchangeAudience = %+v, %v; want an access token", out, err)
	}
	if f.minter.calls != 1 {
		t.Errorf("minted %d access tokens, want 1", f.minter.calls)
	}
}

// Each way an exchange must be refused mints nothing and answers the code
// the caller can act on.
func TestExchangeAudience_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(*exchangeFixture)
		want  connect.Code
	}{
		{"no refresh token", func(f *exchangeFixture) { f.in.RefreshToken = "" }, connect.CodeInvalidArgument},
		{"no audience", func(f *exchangeFixture) { f.in.TargetAudience = "" }, connect.CodeInvalidArgument},
		{"unknown audience", func(f *exchangeFixture) { f.in.TargetAudience = "paladin-nowhere" }, connect.CodeInvalidArgument},
		{"token does not decode", func(f *exchangeFixture) { f.decoder.err = errors.New("bad signature") }, connect.CodeUnauthenticated},
		{"token not on record", func(f *exchangeFixture) { f.refresh.getErr = authstore.ErrNotFound }, connect.CodeUnauthenticated},
		{"token store down", func(f *exchangeFixture) { f.refresh.getErr = errors.New("connection reset") }, connect.CodeInternal},
		{"token expired", func(f *exchangeFixture) { f.refresh.getTok.ExpiresAt = time.Now().Add(-time.Minute) }, connect.CodeUnauthenticated},
		{"token issued to an OAuth client", func(f *exchangeFixture) {
			f.refresh.getTok.OAuth = &authstore.OAuthGrant{}
		}, connect.CodeOf(errNotAConsoleToken)},
		{"user gone", func(f *exchangeFixture) { f.users.err = authstore.ErrNotFound }, connect.CodeUnauthenticated},
		{"user disabled", func(f *exchangeFixture) { f.users.user.Disabled = true }, connect.CodePermissionDenied},
		{"user now in another tenant", func(f *exchangeFixture) { f.users.user.TenantID = uuid.New() }, connect.CodePermissionDenied},
		{"role too low for the admin plane", func(f *exchangeFixture) {
			f.users.user.Roles = []string{apiutil.RoleTenantUser}
		}, connect.CodePermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newExchangeFixture()
			tc.spoil(f)
			_, err := f.run()
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("code = %v (%v), want %v", connect.CodeOf(err), err, tc.want)
			}
			if f.minter.calls != 0 {
				t.Errorf("a refused exchange minted %d access tokens", f.minter.calls)
			}
		})
	}
}

// A page token round-trips the position it was made from; anything else is
// refused, never read as the first page.
func TestMembershipCursor(t *testing.T) {
	created := time.Date(2026, 11, 17, 12, 0, 0, 123, time.UTC)
	id := uuid.New()
	gotAt, gotID, err := decodeMembershipCursor(encodeMembershipCursor(created, id))
	if err != nil || !gotAt.Equal(created) || gotID != id {
		t.Fatalf("round trip = %v, %v, %v; want %v, %v", gotAt, gotID, err, created, id)
	}
	if at, first, err := decodeMembershipCursor(""); err != nil || !at.IsZero() || first != uuid.Nil() {
		t.Fatalf("empty token = %v, %v, %v; want the start", at, first, err)
	}
	for _, bad := range []string{"no-separator", "x:" + id.String(), "123:not-a-uuid"} {
		if _, _, err := decodeMembershipCursor(bad); err == nil {
			t.Errorf("decodeMembershipCursor(%q) accepted a malformed token", bad)
		}
	}
}

// RefreshToken refuses what ExchangeAudience refuses, and rotates nothing
// when it does.
func TestRefreshToken_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(*exchangeFixture)
		want  connect.Code
	}{
		{"no refresh token", func(f *exchangeFixture) { f.in.RefreshToken = "" }, connect.CodeInvalidArgument},
		{"token does not decode", func(f *exchangeFixture) { f.decoder.err = errors.New("bad signature") }, connect.CodeUnauthenticated},
		{"token not on record", func(f *exchangeFixture) { f.refresh.getErr = authstore.ErrNotFound }, connect.CodeUnauthenticated},
		{"token store down", func(f *exchangeFixture) { f.refresh.getErr = errors.New("connection reset") }, connect.CodeInternal},
		{"token expired", func(f *exchangeFixture) { f.refresh.getTok.ExpiresAt = time.Now().Add(-time.Minute) }, connect.CodeUnauthenticated},
		{"user gone", func(f *exchangeFixture) { f.users.err = authstore.ErrNotFound }, connect.CodeUnauthenticated},
		{"user disabled", func(f *exchangeFixture) { f.users.user.Disabled = true }, connect.CodePermissionDenied},
		{"user now in another tenant", func(f *exchangeFixture) { f.users.user.TenantID = uuid.New() }, connect.CodePermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newExchangeFixture()
			tc.spoil(f)
			h := NewHandler(f.users, f.refresh, f.minter, f.decoder, allowAuthorizer{})
			_, err := h.RefreshToken(context.Background(), RefreshInput{RefreshToken: f.in.RefreshToken})
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("code = %v (%v), want %v", connect.CodeOf(err), err, tc.want)
			}
			if f.refresh.inserts != 0 || f.minter.calls != 0 {
				t.Errorf("a refused refresh stored %d tokens and minted %d", f.refresh.inserts, f.minter.calls)
			}
		})
	}
}

// A token store that fails is logged on the request's logger, naming the
// operation; the caller only sees internal.
func TestInternalAuthFailuresAreLogged(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	ctx := logger.WithContext(context.Background(), zap.New(core))
	f := newExchangeFixture()
	f.refresh.getErr = errors.New("connection reset")
	h := NewHandler(f.users, f.refresh, f.minter, f.decoder, allowAuthorizer{})
	if _, err := h.ExchangeAudience(ctx, f.in); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("err = %v, want internal", err)
	}
	lines := logs.All()
	if len(lines) != 1 || lines[0].ContextMap()["op"] != "exchange audience" {
		t.Fatalf("want one error line naming the op, got %v", lines)
	}
}
