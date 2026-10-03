package main

import "testing"

func TestFlavourObjectCount(t *testing.T) {
	cases := []struct {
		flavour  string
		override int
		want     int
	}{
		{"load", 0, 500},    // default
		{"stress", 0, 1001}, // just past a 1000-row page
		{"load", 42, 42},    // override wins
		{"stress", 7, 7},    // override wins
		{"demo", 0, 0},      // not an object flavour
		{"anything", 0, 0},  // unknown → 0
		{"load", -3, 500},   // non-positive override ignored → default
	}
	for _, tc := range cases {
		if got := flavourObjectCount(tc.flavour, tc.override); got != tc.want {
			t.Errorf("flavourObjectCount(%q,%d) = %d, want %d", tc.flavour, tc.override, got, tc.want)
		}
	}
}

func TestFixtureCollection_DeterministicAndSortable(t *testing.T) {
	// Zero-padded so lexical order == numeric order (the UI cursor walks them
	// in key order).
	if got := fixtureCollection("load", 42); got != "fixture/load/000042.txt" {
		t.Errorf("fixtureCollection = %q", got)
	}
	if got := fixtureCollection("stress", 7); got != "fixture/stress/000007.txt" {
		t.Errorf("fixtureCollection = %q", got)
	}
	// Lexical < holds across an order-of-magnitude boundary (9 vs 10).
	if fixtureCollection("load", 9) >= fixtureCollection("load", 10) {
		t.Error("zero-padding must keep 9 < 10 lexically")
	}
	// Distinct flavours never collide.
	if fixtureCollection("load", 1) == fixtureCollection("stress", 1) {
		t.Error("load and stress keys must not collide")
	}
}

func TestIsFixtureCollection(t *testing.T) {
	if !isFixtureCollection("load", "fixture/load/000001.txt") {
		t.Error("own fixture key should match")
	}
	// A different flavour's key is NOT this flavour's — teardown must not
	// delete the other flavour's objects.
	if isFixtureCollection("load", "fixture/stress/000001.txt") {
		t.Error("stress key must not match the load prefix")
	}
	// A real user object is never treated as a fixture.
	if isFixtureCollection("load", "invoices/2026/q1.pdf") {
		t.Error("a non-fixture user key must not match")
	}
	// The key round-trips through its own predicate.
	if !isFixtureCollection("stress", fixtureCollection("stress", 999)) {
		t.Error("fixtureCollection output must satisfy isFixtureCollection")
	}
}

// The fixture's uploads are signed for their checksum; it must be the
// base64 SHA-256 the store compares the body with.
func TestSHA256Base64(t *testing.T) {
	if got := sha256Base64(nil); got != "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=" {
		t.Fatalf("sha256Base64(empty) = %q", got)
	}
}
