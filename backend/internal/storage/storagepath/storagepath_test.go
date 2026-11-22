package storagepath

import (
	"errors"
	"strings"
	"testing"
	"uuid"
)

var testTenant = uuid.MustParse("00000000-0000-0000-0000-0000000000aa")

func TestValidateKey(t *testing.T) {
	for _, tc := range []struct {
		key    string
		reason string // "" accepts
	}{
		{"clean", ""},
		{"a/b/c", ""},
		{".hidden", ""},
		{"a/..b/c.", ""},
		{"...", ""},
		{"0190a3b2-7c4d-7e8f-9a0b-1c2d3e4f5a6b", ""},
		{"", reasonEmpty},
		{"/", reasonLeading},
		{"/leading", reasonLeading},
		{"trailing/", reasonTrailing},
		{"a//b", reasonEmptySegment},
		{".", reasonCurrentSegment},
		{"./rel", reasonCurrentSegment},
		{"a/./b", reasonCurrentSegment},
		{"..", reasonParentSegment},
		{"../escape", reasonParentSegment},
		{"a/../b", reasonParentSegment},
		{"a/..", reasonParentSegment},
	} {
		err := ValidateKey(tc.key)
		assertRefusal(t, "ValidateKey", tc.key, err, KeyRule, tc.reason)
	}
}

func TestValidateKeyPrefix(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		reason string
	}{
		{"", ""},
		{"backup/", ""},
		{"backup/2026/", ""},
		{"copy-of-", ""},
		{"a/.", ""},  // completed by the key: "a/.x"
		{"a/..", ""}, // "a/..x"
		{"/backup/", reasonLeading},
		{"a//", reasonEmptySegment},
		{"./a/", reasonCurrentSegment},
		{"a/../", reasonParentSegment},
	} {
		err := ValidateKeyPrefix(tc.prefix)
		assertRefusal(t, "ValidateKeyPrefix", tc.prefix, err, KeyPrefixRule, tc.reason)
	}
}

func TestValidateCollection(t *testing.T) {
	for _, tc := range []struct {
		collection string
		ok         bool
	}{
		{"docs", true},
		{"invoices/2026/q1", true},
		{"a-b/c-d", true},
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 64), false},
		{strings.Repeat(strings.Repeat("a", 63)+"/", 3) + strings.Repeat("a", 63), true}, // 255 in all
		{strings.Repeat(strings.Repeat("a", 63)+"/", 4) + "a", false},                    // 257
		{"", false},
		{"a.b", false},
		{"A", false},
		{"-a", false},
		{"a-", false},
		{"a//b", false},
		{"/a", false},
		{"a/", false},
		{"../other", false},
	} {
		err := ValidateCollection(tc.collection)
		reason := reasonGrammar
		if tc.ok {
			reason = ""
		}
		assertRefusal(t, "ValidateCollection", tc.collection, err, CollectionRule, reason)
	}
}

func assertRefusal(t *testing.T, fn, in string, err error, rule, reason string) {
	t.Helper()
	if reason == "" {
		if err != nil {
			t.Errorf("%s(%q) = %v, want nil", fn, in, err)
		}
		return
	}
	var e *Error
	if !errors.As(err, &e) || !errors.Is(err, ErrNotCanonical) {
		t.Fatalf("%s(%q) = %v, want a refusal", fn, in, err)
	}
	if e.Rule != rule || e.Reason != reason {
		t.Errorf("%s(%q) = %q / %q, want %q / %q", fn, in, e.Rule, e.Reason, rule, reason)
	}
}

func TestCollectionSegmentRoundTrips(t *testing.T) {
	for _, c := range []string{"docs", "invoices/2026/q1", "a-b/c"} {
		seg := CollectionSegment(c)
		if strings.Contains(seg, Separator) {
			t.Errorf("CollectionSegment(%q) = %q holds %q", c, seg, Separator)
		}
		got, err := ParseCollectionSegment(seg)
		if err != nil || got != c {
			t.Errorf("ParseCollectionSegment(%q) = %q, %v, want %q", seg, got, err, c)
		}
	}
	for _, seg := range []string{"", "a/b", "a..b", ".a", "A"} {
		if _, err := ParseCollectionSegment(seg); !errors.Is(err, ErrNotCanonical) {
			t.Errorf("ParseCollectionSegment(%q) = %v, want a refusal", seg, err)
		}
	}
}

func TestCompose(t *testing.T) {
	prefix := testTenant.String() + Separator
	for _, tc := range []struct {
		collection, key, want string
	}{
		{"docs", "a/b/c", prefix + "docs/a/b/c"},
		{"invoices/2026/q1", "report.pdf", prefix + "invoices.2026.q1/report.pdf"},
		{"invoices", "2026/q1/report.pdf", prefix + "invoices/2026/q1/report.pdf"},
	} {
		got, err := Compose(testTenant, tc.collection, tc.key)
		if err != nil || got != tc.want {
			t.Errorf("Compose(%q, %q) = %q, %v, want %q", tc.collection, tc.key, got, err, tc.want)
		}
	}
}

// The inputs the old composer rewrote are refused, not rewritten: each of
// them shared a path with another object.
func TestComposeRefusesWhatItUsedToRewrite(t *testing.T) {
	for _, tc := range [][2]string{
		{"docs", "a//b"},
		{"docs", "a/./b"},
		{"docs", "/a/b"},
		{"docs", ".."},
		{"docs", ""},
		{"../other", "x"},
		{"docs/", "x"},
		{"", "x"},
	} {
		if got, err := Compose(testTenant, tc[0], tc[1]); !errors.Is(err, ErrNotCanonical) {
			t.Errorf("Compose(%q, %q) = %q, %v, want a refusal", tc[0], tc[1], got, err)
		}
	}
}
