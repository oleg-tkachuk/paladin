package statemachine

import (
	"errors"
	"testing"
)

// A promote places the object in the bucket its upload is anchored to, and
// refuses bytes confirmed anywhere else: they are not the row's (ADR-0051).
func TestPromotePlacesTheObjectInItsAnchor(t *testing.T) {
	elsewhere := Placement{BackendID: "be-1", Bucket: "dedicated"}
	otherBackend := Placement{BackendID: "be-2", Bucket: anchorPlacement.Bucket}
	cases := []struct {
		name        string
		row         fakeRow
		placed      *Placement
		sequencer   string
		wantChanged bool
		wantRefused bool
	}{
		{"confirmed in the anchor", fakeRow{state: string(StatePending)}, &anchorPlacement, "", true, false},
		{"naming no bucket", fakeRow{state: string(StatePending)}, nil, "seq-1", true, false},
		{"confirmed in another bucket", fakeRow{state: string(StatePending)}, &elsewhere, "", false, true},
		{"confirmed on another backend", fakeRow{state: string(StatePending)}, &otherBackend, "", false, true},
		{"a pending upload with no anchor", fakeRow{state: string(StatePending), unanchored: true}, &anchorPlacement, "", false, true},
		{"a settled object reported written again", fakeRow{state: string(StateAvailable), unanchored: true}, &anchorPlacement, "seq-2", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex := &fakeExec{lockRow: tc.row, row: fakeRow{state: string(StateAvailable)}}
			changed, err := (&Transitioner{}).promote(smCtx, ex, objID, tc.placed, "etag", 1, "", tc.sequencer, SourceRPC)
			if tc.wantRefused {
				var na *NotAnchoredError
				if !errors.Is(err, ErrNotAnchored) || !errors.As(err, &na) {
					t.Fatalf("err = %v, want ErrNotAnchored", err)
				}
				if ex.querySQL != "" {
					t.Fatal("a refused promote ran the UPDATE")
				}
				return
			}
			if err != nil {
				t.Fatalf("promote: %v", err)
			}
			if changed != tc.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tc.wantChanged)
			}
			if !tc.wantChanged && ex.querySQL != "" {
				t.Fatal("a settled object's promote ran the UPDATE")
			}
		})
	}
}

// A copy's promote settles the anchor with it: no URL of the row can write
// the key; an upload's keeps it until its URL expires.
func TestPromoteSettlesTheAnchorOfACopyOnly(t *testing.T) {
	for _, settled := range []bool{false, true} {
		placed := anchorPlacement
		placed.Settled = settled
		ex := &fakeExec{lockRow: fakeRow{state: string(StatePending)}, row: fakeRow{state: string(StateAvailable)}}
		if _, err := (&Transitioner{}).promote(smCtx, ex, objID, &placed, "etag", 1, "", "", SourceRPC); err != nil {
			t.Fatalf("promote: %v", err)
		}
		if got := ex.queryArgs[5]; got != settled {
			t.Errorf("settled arg = %v, want %v", got, settled)
		}
	}
}

func TestNotAnchoredErrorNamesBothBuckets(t *testing.T) {
	err := &NotAnchoredError{Placed: Placement{BackendID: "be-1", Bucket: "dedicated"}, Anchor: anchorPlacement}
	if want := `the bytes are not in the bucket the object's upload is anchored to: bytes in be-1/dedicated, anchored to "be-1"/"shared"`; err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestNormalizeETagDropsTheWireQuotes(t *testing.T) {
	for in, want := range map[string]string{`"abc"`: "abc", "abc": "abc", `"abc-2"`: "abc-2", "": ""} {
		if got := NormalizeETag(in); got != want {
			t.Errorf("NormalizeETag(%q) = %q, want %q", in, got, want)
		}
	}
}
