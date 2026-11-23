package usersettingsh

import (
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
)

// updateCodes is what UpdateMine answers settings it refuses with: a mask
// field, timezone, locale, theme or preferences document it cannot take.
var updateCodes = []connect.Code{connect.CodeInvalidArgument}

// Whatever a user sets, UpdateMine stores settings the user_settings CHECKs
// accept — a locale of 2 to 35 bytes, a declared theme — or refuses them as
// the caller's mistake, in words that name nothing of the server.
func FuzzUpdateMine(f *testing.F) {
	for _, c := range [][5]string{
		{FieldTheme, "UTC", "en", "dark", "{}"},
		{FieldTimezone, "Europe/Kyiv", "uk-UA", "system", `{"a":1}`},
		{FieldLocale, "", "uk-UA", "", ""},
		{FieldPreferences, "", "", "", `{"a":`},
		{"nope", "Mars/Base", "x", "neon", "[]"},
		{FieldLocale, "", "a\x00", "", ""},
	} {
		f.Add(c[0], c[1], c[2], c[3], []byte(c[4]))
	}
	uid, tid := uuid.New(), uuid.New()
	ctx := ctxAsUser(uid, tid)
	f.Fuzz(func(t *testing.T, field, timezone, locale, theme string, preferences []byte) {
		repo := &fakeRepo{getErr: ErrNotFound}
		h := NewHandler(repo, &fakeUsers{user: authstore.User{UserID: uid, TenantID: tid}}, allowAuthorizer{})
		_, err := h.UpdateMine(ctx, UpdateMineInput{
			UpdateMask: []string{field}, Timezone: timezone, Locale: locale, Theme: theme, Preferences: preferences,
		})
		fuzzassert.Refusal(t, err, updateCodes, field, timezone, locale, theme, string(preferences))
		if err != nil {
			return
		}
		// What the user_settings CHECKs hold the row to.
		if apiutil.ValidateLocale(repo.upserted.Locale) != nil || apiutil.ValidateTheme(repo.upserted.Theme) != nil {
			t.Fatalf("settings %+v reached the store", repo.upserted)
		}
	})
}
