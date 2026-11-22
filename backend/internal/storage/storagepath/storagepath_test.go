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

// invalidUTF8 is a byte no UTF-8 sequence starts with.
const invalidUTF8 = "\xff"

// longestCollection is a collection of the grammar's greatest length.
var longestCollection = strings.Repeat(strings.Repeat("a", 63)+"/", 3) + strings.Repeat("a", 63)

func TestMaxKeyBytesLeavesRoomForTheLongestCollection(t *testing.T) {
	if len(longestCollection) != maxCollectionLength || ValidateCollection(longestCollection) != nil {
		t.Fatalf("the longest collection is %d bytes, want a valid one of %d", len(longestCollection), maxCollectionLength)
	}
	if len(testTenant.String()) != tenantSegmentBytes {
		t.Fatalf("a tenant id is %d bytes, tenantSegmentBytes says %d", len(testTenant.String()), tenantSegmentBytes)
	}
	if len(CollectionSegment(longestCollection)) != MaxSegmentBytes {
		t.Fatalf("the longest collection segment is %d bytes, want MaxSegmentBytes", len(CollectionSegment(longestCollection)))
	}
	got, err := Compose(testTenant, longestCollection, longKey(MaxKeyBytes))
	if err != nil || len(got) != MaxPathBytes {
		t.Fatalf("the longest key in the longest collection composes to %d bytes, %v; want exactly %d", len(got), err, MaxPathBytes)
	}
}

// longKey is a key of n bytes in segments of at most MaxSegmentBytes.
func longKey(n int) string {
	var b strings.Builder
	for n > MaxSegmentBytes {
		b.WriteString(strings.Repeat("k", MaxSegmentBytes) + Separator)
		n -= MaxSegmentBytes + len(Separator)
	}
	b.WriteString(strings.Repeat("k", n))
	return b.String()
}

func TestValidateKeyBoundsTheText(t *testing.T) {
	tooLong := tooLongReason(MaxKeyBytes+1, MaxKeyBytes)
	// 128 two-byte runes: under the segment limit in characters, over it in bytes.
	twoByteSegment := strings.Repeat("é", (MaxSegmentBytes+1)/len("é"))
	for _, tc := range []struct {
		name, key, reason string
	}{
		{"at the limit", longKey(MaxKeyBytes), ""},
		{"over the limit", longKey(MaxKeyBytes + 1), tooLong},
		// 366 two-byte runes: under the limit in characters, over it in bytes.
		{"over the limit in bytes", strings.Repeat("é/", (MaxKeyBytes+1)/len("é/")), tooLong},
		{"a segment at the limit", strings.Repeat("s", MaxSegmentBytes), ""},
		{"a segment over the limit", "a/" + strings.Repeat("s", MaxSegmentBytes+1), segmentLengthReason([]string{strings.Repeat("s", MaxSegmentBytes+1)}, MaxSegmentBytes)},
		{"a segment over the limit in bytes", twoByteSegment, segmentLengthReason([]string{twoByteSegment}, MaxSegmentBytes)},
		{"non-ASCII", "résumé/отчёт.pdf", ""},
		{"NUL", "a\x00b", reasonControl},
		{"newline", "a\nb", reasonControl},
		{"DEL", "a\x7fb", reasonControl},
		{"C1 control", "a\u0085b", reasonControl},
		{"invalid UTF-8", "a" + invalidUTF8, reasonInvalidUTF8},
		{"lone surrogate", "a\xed\xa0\x80", reasonInvalidUTF8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRefusal(t, "ValidateKey", tc.key, ValidateKey(tc.key), KeyRule, tc.reason)
		})
	}
}

func TestValidateKeyPrefixLeavesRoomForAKey(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, reason string
	}{
		{"one byte short of the limit", longKey(MaxKeyBytes - 1), ""},
		{"at the key limit", longKey(MaxKeyBytes), tooLongReason(MaxKeyBytes, MaxKeyBytes-1)},
		{"a complete segment at the limit", strings.Repeat("s", MaxSegmentBytes) + Separator, ""},
		{"a complete segment over the limit", strings.Repeat("s", MaxSegmentBytes+1) + Separator,
			segmentLengthReason([]string{strings.Repeat("s", MaxSegmentBytes+1)}, MaxSegmentBytes)},
		{"a last segment with room for a byte", strings.Repeat("s", MaxSegmentBytes-1), ""},
		{"a last segment with no room", strings.Repeat("s", MaxSegmentBytes),
			segmentLengthReason([]string{strings.Repeat("s", MaxSegmentBytes)}, MaxSegmentBytes-1)},
		{"control character", "a\tb/", reasonControl},
		{"invalid UTF-8", invalidUTF8 + "/", reasonInvalidUTF8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRefusal(t, "ValidateKeyPrefix", tc.prefix, ValidateKeyPrefix(tc.prefix), KeyPrefixRule, tc.reason)
		})
	}
}

// An object stored before the length and control-character rules keeps its
// path, so it stays readable; what the store cannot hold or a reader would
// resolve elsewhere is still refused.
func TestComposeBoundsThePathNotTheBoundaryRules(t *testing.T) {
	legacyLong := strings.Repeat("a", MaxKeyBytes+1)
	for _, key := range []string{legacyLong, "a\nb"} {
		if _, err := Compose(testTenant, "docs", key); err != nil {
			t.Errorf("Compose(docs, %q) = %v, want the legacy key composed", key, err)
		}
	}
	for _, key := range []string{legacyLong, "a\nb", strings.Repeat("s", MaxSegmentBytes+1)} {
		if err := ValidateStoredKey(key); err != nil || ValidateKey(key) == nil {
			t.Errorf("%q: stored %v, admitted %v; want stored and not admitted", key, err, ValidateKey(key))
		}
	}
	for _, key := range []string{"a//b", "..", "a" + invalidUTF8} {
		if !errors.Is(ValidateStoredKey(key), ErrNotCanonical) {
			t.Errorf("ValidateStoredKey(%q) accepted a key with no path of its own", key)
		}
	}
	if _, err := Compose(testTenant, "docs", "a"+invalidUTF8); !errors.Is(err, ErrNotCanonical) {
		t.Errorf("Compose of invalid UTF-8 = %v, want a refusal", err)
	}
	_, err := Compose(testTenant, longestCollection, legacyLong)
	var e *Error
	if !errors.As(err, &e) || e.Rule != PathRule {
		t.Errorf("Compose past %d bytes = %v, want a %q refusal", MaxPathBytes, err, PathRule)
	}
}
