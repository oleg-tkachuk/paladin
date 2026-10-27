package storagepath

import (
	"testing"
	"uuid"
)

func TestSanitizeSegment(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"clean":            "clean",
		"a/b/c":            "a/b/c",   // multi-level keys preserved
		"../escape":        "escape",  // dotdot stripped
		"/leading":         "leading", // leading slash stripped
		"a/../b":           "a/b",     // internal dotdot stripped
		"./rel":            "rel",     // dot stripped
		"a//b":             "a/b",     // empty component collapsed
		"../../etc/passwd": "etc/passwd",
	}
	for in, want := range cases {
		if got := sanitizeSegment(in); got != want {
			t.Errorf("sanitizeSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComposeCannotEscapeTenantPrefix(t *testing.T) {
	tid := uuid.MustParse("00000000-0000-0000-0000-0000000000aa")
	got := Compose(tid, "../other", "../../x")
	want := tid.String() + "/other/x"
	if got != want {
		t.Errorf("Compose traversal: got %q, want %q", got, want)
	}
}
