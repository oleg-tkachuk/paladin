package cel

import "testing"

// The same table the console pins in src/lib/cel.test.ts.
//
// Three spellings of one definition — this, the SQL in ListBucketsV2, and
// asciiLower() in the console — and only two of them can be checked against
// each other by a machine (the integration test does that). This side and the
// console side are kept honest by asserting the same rows in both, so a change
// to one shows up as a failure in the other's language rather than as a search
// that quietly finds nothing.
func TestAsciiLowerFoldsOnlyASCII(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Prod-LOGS", "prod-logs"},
		{"ÜBER Cache", "Über cache"},
		{"İstanbul", "İstanbul"},
		{"100% DONE", "100% done"},
	} {
		if got := asciiLower(c.in); got != c.want {
			t.Errorf("asciiLower(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The joined shape, including the separator that keeps a query from matching
// across a field boundary.
func TestSearchTextJoinsWithANewline(t *testing.T) {
	got := SearchText("Prod-Logs", "The Production Bucket")
	want := "prod-logs\nthe production bucket"
	if got != want {
		t.Errorf("SearchText = %q, want %q", got, want)
	}
	// An empty display name still produces the separator, matching
	// coalesce(display_name,'') in the SQL. Without this the two definitions
	// differ for every bucket that has no display name, which is most of them
	// in a fresh deployment.
	if got, want := SearchText("solo", ""), "solo\n"; got != want {
		t.Errorf("SearchText with empty second part = %q, want %q", got, want)
	}
}
