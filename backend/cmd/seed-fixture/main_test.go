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

func TestFixtureObjectKey_DeterministicAndSortable(t *testing.T) {
	// Zero-padded so lexical order == numeric order (the UI cursor walks them
	// in key order).
	if got := fixtureObjectKey("load", 42); got != "fixture/load/000042.txt" {
		t.Errorf("fixtureObjectKey = %q", got)
	}
	if got := fixtureObjectKey("stress", 7); got != "fixture/stress/000007.txt" {
		t.Errorf("fixtureObjectKey = %q", got)
	}
	// Lexical < holds across an order-of-magnitude boundary (9 vs 10).
	if fixtureObjectKey("load", 9) >= fixtureObjectKey("load", 10) {
		t.Error("zero-padding must keep 9 < 10 lexically")
	}
	// Distinct flavours never collide.
	if fixtureObjectKey("load", 1) == fixtureObjectKey("stress", 1) {
		t.Error("load and stress keys must not collide")
	}
}

func TestIsFixtureObjectKey(t *testing.T) {
	if !isFixtureObjectKey("load", "fixture/load/000001.txt") {
		t.Error("own fixture key should match")
	}
	// A different flavour's key is NOT this flavour's — teardown must not
	// delete the other flavour's objects.
	if isFixtureObjectKey("load", "fixture/stress/000001.txt") {
		t.Error("stress key must not match the load prefix")
	}
	// A real user object is never treated as a fixture.
	if isFixtureObjectKey("load", "invoices/2026/q1.pdf") {
		t.Error("a non-fixture user key must not match")
	}
	// The key round-trips through its own predicate.
	if !isFixtureObjectKey("stress", fixtureObjectKey("stress", 999)) {
		t.Error("fixtureObjectKey output must satisfy isFixtureObjectKey")
	}
}
