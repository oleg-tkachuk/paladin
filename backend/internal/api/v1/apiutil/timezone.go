package apiutil

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// localeRE mirrors the loose CHECK in migrations/010_user_settings.sql.
// We allow both BCP-47 hyphenated form (`uk-UA`, `pt-BR`) and the older
// underscore form (`uk_UA`) that some clients still emit.
var localeRE = regexp.MustCompile(`^[A-Za-z]{2,3}([-_][A-Za-z0-9]{2,8})*$`)

// ValidateTimezone returns nil iff `s` is a recognized IANA tz database
// name. Resolution is delegated to time.LoadLocation so the universe of
// accepted names tracks the Go stdlib's tzdata bundle (or the OS zoneinfo).
//
// "" is rejected — callers must explicitly opt into "UTC" rather than relying
// on a zero-value default that would render every timestamp ambiguously.
func ValidateTimezone(s string) error {
	if s == "" {
		return errors.New("timezone must not be empty (use \"UTC\" if unset)")
	}
	if len(s) > 64 {
		return fmt.Errorf("timezone too long (%d chars, max 64)", len(s))
	}
	if _, err := time.LoadLocation(s); err != nil {
		return fmt.Errorf("unknown timezone %q: %w", s, err)
	}
	return nil
}

// ValidateLocale returns nil iff `s` looks like a BCP-47 language tag.
// We don't validate against the IANA registry — operators sometimes ship
// custom tags ("x-internal-fr") and the cost of stale registry data is not
// worth the precision.
func ValidateLocale(s string) error {
	if s == "" {
		return errors.New("locale must not be empty")
	}
	if len(s) < 2 || len(s) > 35 {
		return fmt.Errorf("locale length out of range (%d, want 2..35)", len(s))
	}
	if !localeRE.MatchString(s) {
		return fmt.Errorf("locale %q is not a valid BCP-47 tag", s)
	}
	return nil
}

// ValidateTheme bounds the theme universe to the values understood by the
// web client. Storing arbitrary strings here would break the UI silently.
func ValidateTheme(s string) error {
	switch s {
	case "light", "dark", "system":
		return nil
	default:
		return fmt.Errorf("theme %q must be one of: light, dark, system", s)
	}
}
