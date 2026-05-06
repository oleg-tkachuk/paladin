package apiutil

import "testing"

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
	for _, ok := range []string{"light", "dark", "system"} {
		if err := ValidateTheme(ok); err != nil {
			t.Errorf("ValidateTheme(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "midnight", "Light", "auto"} {
		if err := ValidateTheme(bad); err == nil {
			t.Errorf("ValidateTheme(%q): want error", bad)
		}
	}
}
