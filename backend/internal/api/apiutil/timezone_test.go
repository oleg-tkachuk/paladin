package apiutil

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/migrations"
)

func TestValidateTimezone(t *testing.T) {
	t.Parallel()
	good := []string{"UTC", "Europe/Kyiv", "America/New_York", "Asia/Tokyo", "Etc/GMT+3"}
	for _, tz := range good {
		if err := ValidateTimezone(tz); err != nil {
			t.Errorf("ValidateTimezone(%q): %v", tz, err)
		}
	}
	bad := []string{"", "Mars/Phobos", "not a tz", "Europe/Kyiv; DROP TABLE", string(make([]byte, 65))}
	for _, tz := range bad {
		if err := ValidateTimezone(tz); err == nil {
			t.Errorf("ValidateTimezone(%q): want error, got nil", tz)
		}
	}
}

func TestValidateLocale(t *testing.T) {
	t.Parallel()
	good := []string{"en", "en-US", "uk-UA", "pt-BR", "zh-Hant-TW", "uk_UA"}
	for _, l := range good {
		if err := ValidateLocale(l); err != nil {
			t.Errorf("ValidateLocale(%q): %v", l, err)
		}
	}
	bad := []string{"", "x", "EN US", "en--US", "123", "en-"}
	for _, l := range bad {
		if err := ValidateLocale(l); err == nil {
			t.Errorf("ValidateLocale(%q): want error, got nil", l)
		}
	}
}

func TestValidateTheme(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"light", "dark", "system", "ember"} {
		if err := ValidateTheme(ok); err != nil {
			t.Errorf("ValidateTheme(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "midnight", "Light", "auto", "violet"} {
		if err := ValidateTheme(bad); err == nil {
			t.Errorf("ValidateTheme(%q): want error", bad)
		}
	}
}

// themeCheckRE captures the IN list of the user_settings theme CHECK.
var themeCheckRE = regexp.MustCompile(`CHECK \(theme IN \(([^)]*)\)\)`)

// The database refuses a theme the API accepts, or the reverse, unless the
// newest migration's CHECK names exactly apiutil.Themes.
func TestThemesMatchMigrationCheck(t *testing.T) {
	t.Parallel()
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var latest []string
	for _, e := range entries {
		body, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(body), "-- +goose Down")
		if m := themeCheckRE.FindStringSubmatch(up); m != nil {
			latest = nil
			for _, v := range strings.Split(m[1], ",") {
				latest = append(latest, strings.Trim(strings.TrimSpace(v), "'"))
			}
		}
	}
	if latest == nil {
		t.Fatal("no migration carries the user_settings theme CHECK")
	}
	got, want := slices.Sorted(slices.Values(latest)), slices.Sorted(slices.Values(Themes))
	if !slices.Equal(got, want) {
		t.Errorf("migration CHECK = %v, apiutil.Themes = %v", got, want)
	}
}
