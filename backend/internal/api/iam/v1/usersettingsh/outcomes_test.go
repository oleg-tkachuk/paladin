package usersettingsh

import (
	"errors"
	"fmt"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// A user store that fails is an outage. It used to read as the caller's
// answer: NotFound for the user named, Unauthenticated for the caller.
func TestUserLookupFailuresAreInternal(t *testing.T) {
	uid, tid := uuid.New(), uuid.New()
	boom := errors.New("connection reset")
	missing := fmt.Errorf("user: %w", authstore.ErrNotFound)
	admin := ctxAsUser(uid, tid, apiutil.RolePlatformAdmin)
	for _, tc := range []struct {
		name  string
		users error
		call  func(*Handler) error
		want  connect.Code
	}{
		{"get for user: not there", missing, func(h *Handler) error { _, err := h.GetForUser(admin, uuid.New()); return err }, connect.CodeNotFound},
		{"get for user: store down", boom, func(h *Handler) error { _, err := h.GetForUser(admin, uuid.New()); return err }, connect.CodeInternal},
		{"delete for user: store down", boom, func(h *Handler) error { return h.DeleteForUser(admin, uuid.New()) }, connect.CodeInternal},
		{"mine: caller gone", missing, func(h *Handler) error {
			_, err := h.GetMine(ctxAsUser(uid, tid))
			return err
		}, connect.CodeUnauthenticated},
		{"mine: store down", boom, func(h *Handler) error {
			_, err := h.GetMine(ctxAsUser(uid, tid))
			return err
		}, connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.ErrorLevel)
			h := NewHandler(&fakeRepo{}, &fakeUsers{err: tc.users}, allowAuthorizer{})
			h.SetLogger(zap.New(core))
			err := tc.call(h)
			if code(err) != tc.want {
				t.Fatalf("code = %v (%v), want %v", code(err), err, tc.want)
			}
			if tc.want == connect.CodeInternal && logs.Len() != 1 {
				t.Fatalf("want one error line, got %v", logs.All())
			}
		})
	}
}

// A list the store fails is internal, not an error with no code.
func TestListStoreFailureIsInternal(t *testing.T) {
	uid, tid := uuid.New(), uuid.New()
	h := NewHandler(&fakeRepo{listErr: errors.New("connection reset")}, &fakeUsers{}, allowAuthorizer{})
	if _, err := h.ListByTenant(ctxAsUser(uid, tid, apiutil.RolePlatformAdmin), tid, 0); code(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want internal", code(err))
	}
}

func TestClampSettingsPageSize(t *testing.T) {
	for _, tc := range []struct{ asked, want int32 }{
		{-1, DefaultListPageSize}, {0, DefaultListPageSize}, {1, 1},
		{MaxListPageSize, MaxListPageSize}, {MaxListPageSize + 1, DefaultListPageSize},
	} {
		if got := clampPageSize(tc.asked); got != tc.want {
			t.Errorf("clampPageSize(%d) = %d, want %d", tc.asked, got, tc.want)
		}
	}
}

// Changing settings is logged, naming whose.
func TestSettingsChangesAreLogged(t *testing.T) {
	uid, tid := uuid.New(), uuid.New()
	core, logs := observer.New(zapcore.InfoLevel)
	h := NewHandler(&fakeRepo{getErr: ErrNotFound}, &fakeUsers{user: authstore.User{UserID: uid, TenantID: tid}}, allowAuthorizer{})
	h.SetLogger(zap.New(core))
	if _, err := h.UpdateMine(ctxAsUser(uid, tid), UpdateMineInput{UpdateMask: []string{FieldTheme}, Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	if err := h.DeleteForUser(ctxAsUser(uid, tid, apiutil.RolePlatformAdmin), uid); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{"user settings updated", "user settings deleted"} {
		lines := logs.FilterMessage(msg).All()
		if len(lines) != 1 || lines[0].ContextMap()["user_id"] != uid.String() {
			t.Errorf("want one %q line naming the user, got %v", msg, logs.All())
		}
	}
}

// An empty mask replaces every field the request carries, and keeps the
// stored preferences when it carries none.
func TestApplyMaskWithoutAMask(t *testing.T) {
	stored := []byte(`{"layout":"wide"}`)
	s := Settings{Timezone: DefaultTimezone, Locale: DefaultLocale, Theme: DefaultTheme, Preferences: stored}
	if err := applyMask(&s, UpdateMineInput{Timezone: "Europe/Kyiv", Locale: "uk-UA", Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	if s.Timezone != "Europe/Kyiv" || s.Locale != "uk-UA" || s.Theme != "dark" || string(s.Preferences) != string(stored) {
		t.Fatalf("settings = %+v", s)
	}
	replaced := []byte(`{"layout":"narrow"}`)
	if err := applyMask(&s, UpdateMineInput{Timezone: "UTC", Locale: "en-US", Theme: "light", Preferences: replaced}); err != nil {
		t.Fatal(err)
	}
	if string(s.Preferences) != string(replaced) {
		t.Fatalf("preferences = %s, want them replaced", s.Preferences)
	}
}
