package publicread_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
)

// A key is 128 random bits in lowercase base32: URL- and S3-safe, and never
// the same twice.
func TestNewKey(t *testing.T) {
	shape := regexp.MustCompile(`^[a-z2-7]{26}$`)
	seen := map[string]bool{}
	const draws = 1000
	for range draws {
		k, err := publicread.NewKey()
		if err != nil {
			t.Fatal(err)
		}
		if !shape.MatchString(k) {
			t.Fatalf("key %q is not 26 lowercase base32 characters", k)
		}
		if seen[k] {
			t.Fatalf("key %q drawn twice", k)
		}
		seen[k] = true
	}
}

func TestIsActive(t *testing.T) {
	for ct, want := range map[string]bool{
		"image/jpeg":                false,
		"image/webp":                false,
		"application/pdf":           false,
		"text/plain; charset=utf-8": false,
		"text/html":                 true,
		"TEXT/HTML; charset=utf-8":  true,
		"image/svg+xml":             true,
		"application/atom+xml":      true,
		"application/javascript":    true,
		"not a type":                true,
	} {
		if got := publicread.IsActive(ct); got != want {
			t.Errorf("IsActive(%q) = %v, want %v", ct, got, want)
		}
	}
}

func TestCheckAllowedTypes(t *testing.T) {
	if err := publicread.CheckAllowedTypes([]string{"image/jpeg", "image/webp"}); err != nil {
		t.Errorf("passive types refused: %v", err)
	}
	for name, types := range map[string][]string{
		"none":         nil,
		"an html type": {"image/jpeg", "text/html"},
		"an svg":       {"image/svg+xml"},
	} {
		if err := publicread.CheckAllowedTypes(types); !errors.Is(err, publicread.ErrRule) {
			t.Errorf("%s: err = %v, want a public collection rule", name, err)
		}
	}
}

func TestCacheControl(t *testing.T) {
	if got, err := publicread.CacheControl(""); err != nil || got != publicread.DefaultCacheControl {
		t.Errorf("empty = %q, %v; want the default", got, err)
	}
	if got, err := publicread.CacheControl("public, max-age=600"); err != nil || got != "public, max-age=600" {
		t.Errorf("a value = %q, %v; want it kept", got, err)
	}
	for name, v := range map[string]string{
		"a newline": "public\r\nX-Injected: 1",
		"too long":  strings.Repeat("a", publicread.MaxCacheControlLength+1),
		"non-ASCII": "public, max-age=60é",
	} {
		if _, err := publicread.CacheControl(v); !errors.Is(err, publicread.ErrRule) {
			t.Errorf("%s: err = %v, want a public collection rule", name, err)
		}
	}
}
