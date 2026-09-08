package adapters

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/usersettingsh"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// refresh_tokens carries four UUID columns in a row — id, user_id, tenant_id,
// family_id — and two adjacent timestamps. Nothing about a transposition here
// is visible at the type level, and every consequence is an authentication
// one: a token whose user and tenant are swapped authenticates the wrong
// principal, and issued/expires the wrong way round makes every token either
// already expired or valid until the epoch runs out.
func TestRefreshTokenFromSQLC(t *testing.T) {
	jti := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	user := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	tenant := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	family := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	issued := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	expires := time.Date(2026, 1, 9, 3, 4, 5, 0, time.UTC)
	superseded := time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)

	row := sqlc.RefreshToken{
		ID:        pgUUID(jti),
		UserID:    pgUUID(user),
		TenantID:  pgUUID(tenant),
		FamilyID:  pgUUID(family),
		IssuedAt:  pgTS(issued),
		ExpiresAt: pgTS(expires),
		Revoked:   true,
	}
	got := refreshTokenFromSQLC(row)

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"JTI", got.JTI, jti},
		{"UserID", got.UserID, user},
		{"TenantID", got.TenantID, tenant},
		{"FamilyID", got.FamilyID, family},
		{"Revoked", got.Revoked, true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if !got.IssuedAt.Equal(issued) {
		t.Errorf("IssuedAt = %v, want %v", got.IssuedAt, issued)
	}
	if !got.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expires)
	}

	// NULL superseded_at is the load-bearing case: the column is set only
	// when a token was rotated for a successor, and stays NULL for tokens
	// revoked for cause. A zero time in the pointer would read as "rotated
	// at the epoch" — reuse detection would treat a revoked token as an
	// ordinary rotation.
	if got.SupersededAt != nil {
		t.Errorf("SupersededAt = %v, want nil for a token that was not rotated", got.SupersededAt)
	}
	row.SupersededAt = pgTS(superseded)
	if got := refreshTokenFromSQLC(row); got.SupersededAt == nil || !got.SupersededAt.Equal(superseded) {
		t.Errorf("SupersededAt = %v, want %v", got.SupersededAt, superseded)
	}
}

// Roles and scopes are the authorisation payload, both decoded from JSONB in
// the same function. Swapped, a user carries their scopes as role names and
// their roles as scopes — and ParseScopes drops what it cannot read, so the
// result is a user with fewer scopes and no error anywhere.
func TestUserFromSQLC(t *testing.T) {
	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	tenant := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	login := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	display := "Ada Lovelace"

	row := sqlc.User{
		ID:              pgUUID(id),
		TenantID:        pgUUID(tenant),
		Subject:         "ada@example.com",
		DisplayName:     &display,
		PasswordHash:    []byte("argon2id$hash"),
		Roles:           []byte(`["admin","auditor"]`),
		Scopes:          []byte(`["tenant:acme","*"]`),
		Disabled:        true,
		ResourceVersion: 9,
		CreatedAt:       pgTS(created),
		UpdatedAt:       pgTS(updated),
		LastLoginAt:     pgTS(login),
	}
	got := userFromSQLC(row)

	if got.UserID != id || got.TenantID != tenant {
		t.Errorf("ids = (%v, %v), want (%v, %v)", got.UserID, got.TenantID, id, tenant)
	}
	if got.Subject != "ada@example.com" || got.DisplayName != display {
		t.Errorf("subject/display = (%q, %q)", got.Subject, got.DisplayName)
	}
	if string(got.PasswordHash) != "argon2id$hash" {
		t.Errorf("PasswordHash = %q", got.PasswordHash)
	}
	if len(got.Roles) != 2 || got.Roles[0] != "admin" || got.Roles[1] != "auditor" {
		t.Errorf("Roles = %v, want [admin auditor]", got.Roles)
	}
	if len(got.Scopes) != 2 {
		t.Fatalf("Scopes = %v, want two entries", got.Scopes)
	}
	if got.Scopes[0] != (auth.Scope{Type: auth.ScopeTenant, Value: "acme"}) {
		t.Errorf("Scopes[0] = %+v, want tenant:acme", got.Scopes[0])
	}
	if got.Scopes[1].Type != auth.ScopeWildcard {
		t.Errorf("Scopes[1] = %+v, want the wildcard", got.Scopes[1])
	}
	if !got.Disabled {
		t.Error("Disabled = false, want true")
	}
	if got.ResourceVersion != 9 {
		t.Errorf("ResourceVersion = %d, want 9", got.ResourceVersion)
	}
	if !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(updated) {
		t.Errorf("timestamps = (%v, %v), want (%v, %v)", got.CreatedAt, got.UpdatedAt, created, updated)
	}
	if got.LastLoginAt == nil || !got.LastLoginAt.Equal(login) {
		t.Errorf("LastLoginAt = %v, want %v", got.LastLoginAt, login)
	}

	// A user who has never signed in has no last-login instant, and the
	// malformed-JSON path degrades to no roles rather than to a panic.
	bare := userFromSQLC(sqlc.User{Roles: []byte(`{not json`), Scopes: []byte(``)})
	if bare.LastLoginAt != nil {
		t.Errorf("LastLoginAt for a user who never logged in = %v, want nil", bare.LastLoginAt)
	}
	if len(bare.Roles) != 0 || len(bare.Scopes) != 0 {
		t.Errorf("malformed JSON produced roles=%v scopes=%v, want neither", bare.Roles, bare.Scopes)
	}
}

// scopesToWire is the return leg of the same trip: what the API hands back
// has to parse into what was stored, or a client that echoes its own scopes
// on the next call loses them.
func TestScopesToWireRoundTrip(t *testing.T) {
	in := []string{"tenant:acme", "bucket:photos", "*"}
	scopes, err := auth.ParseScopes(in)
	if err != nil {
		t.Fatalf("ParseScopes: %v", err)
	}
	got := scopesToWire(scopes)
	if len(got) != len(in) {
		t.Fatalf("scopesToWire returned %d entries, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("scopesToWire[%d] = %q, want %q", i, got[i], in[i])
		}
	}

	// Empty in, empty out — and non-nil, because a nil slice marshals to
	// JSON null where the field is documented as a list.
	if got := scopesToWire(nil); got == nil || len(got) != 0 {
		t.Errorf("scopesToWire(nil) = %v, want an empty non-nil slice", got)
	}
}

// Three adjacent free-text strings the user chose, and one JSONB blob.
func TestSettingsFromSQLC(t *testing.T) {
	user := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	tenant := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

	got := settingsFromSQLC(sqlc.UserSetting{
		UserID:          pgUUID(user),
		TenantID:        pgUUID(tenant),
		Timezone:        "Europe/Kyiv",
		Locale:          "uk-UA",
		Theme:           "dark",
		Preferences:     []byte(`{"density":"compact"}`),
		ResourceVersion: 3,
		CreatedAt:       pgTS(created),
		UpdatedAt:       pgTS(updated),
	})

	want := usersettingsh.Settings{
		UserID:          user,
		TenantID:        tenant,
		Timezone:        "Europe/Kyiv",
		Locale:          "uk-UA",
		Theme:           "dark",
		ResourceVersion: 3,
	}
	if got.UserID != want.UserID || got.TenantID != want.TenantID {
		t.Errorf("ids = (%v, %v), want (%v, %v)", got.UserID, got.TenantID, want.UserID, want.TenantID)
	}
	if got.Timezone != want.Timezone || got.Locale != want.Locale || got.Theme != want.Theme {
		t.Errorf("strings = (%q, %q, %q), want (%q, %q, %q)",
			got.Timezone, got.Locale, got.Theme, want.Timezone, want.Locale, want.Theme)
	}
	if string(got.Preferences) != `{"density":"compact"}` {
		t.Errorf("Preferences = %s", got.Preferences)
	}
	if got.ResourceVersion != want.ResourceVersion {
		t.Errorf("ResourceVersion = %d, want %d", got.ResourceVersion, want.ResourceVersion)
	}
	if !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(updated) {
		t.Errorf("timestamps = (%v, %v), want (%v, %v)", got.CreatedAt, got.UpdatedAt, created, updated)
	}
}
