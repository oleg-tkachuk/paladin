package statemachine

import (
	"errors"
	"testing"
)

// registrationRow answers the registration check: the row's state and what
// the object was registered with.
type registrationRow struct {
	state    State
	size     *int64
	checksum *string
}

func (r registrationRow) Scan(dest ...any) error {
	*dest[0].(*string) = string(r.state)
	*dest[1].(**int64) = r.size
	*dest[2].(**string) = r.checksum
	return nil
}

func i64(v int64) *int64   { return &v }
func str(v string) *string { return &v }

// The first promote of a PENDING object used to accept whatever the store
// reported: an upload admitted as 1 KiB — by Cedar, by quota, by the URL —
// became AVAILABLE at 5 GiB. It is now refused unless the stored bytes are
// the registered ones, and the row is left PENDING for the caller to settle.
func TestPromoteHoldsAPendingObjectToItsRegistration(t *testing.T) {
	cases := []struct {
		name      string
		row       registrationRow
		size      int64
		checksum  string
		sequencer string
		mismatch  string // "" → promotes
	}{
		{"matching size and checksum", registrationRow{StatePending, i64(10), str("abc=")}, 10, "abc=", "", ""},
		{"an empty object registered as empty", registrationRow{StatePending, i64(0), nil}, 0, "", "", ""},
		{"a larger object than registered", registrationRow{StatePending, i64(10), nil}, 11, "", "", "size"},
		{"a smaller object than registered", registrationRow{StatePending, i64(10), nil}, 9, "", "", "size"},
		{"a zero-byte HEAD against a registered size", registrationRow{StatePending, i64(10), nil}, 0, "", "", "size"},
		{"a different checksum", registrationRow{StatePending, i64(10), str("abc=")}, 10, "xyz=", "", "checksum"},
		{"no checksum reported cannot contradict", registrationRow{StatePending, i64(10), str("abc=")}, 10, "", "", ""},
		{"no size registered is not checked", registrationRow{StatePending, nil, nil}, 999, "", "", ""},
		{"an event omitting the size is not refused", registrationRow{StatePending, i64(10), nil}, 0, "", "seq-1", ""},
		{"an event with a wrong size is refused", registrationRow{StatePending, i64(10), nil}, 12, "", "seq-1", "size"},
		{"an already promoted object is not re-checked", registrationRow{StateAvailable, i64(10), str("abc=")}, 99, "zzz=", "seq-2", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := &Transitioner{}
			ex := &fakeExec{lockRow: tc.row, row: fakeRow{state: string(StateAvailable)}}
			changed, err := sm.promote(smCtx, ex, objID, "etag", tc.size, tc.checksum, tc.sequencer, SourceRPC)
			if tc.mismatch == "" {
				if err != nil || !changed {
					t.Fatalf("changed=%v err=%v, want a promote", changed, err)
				}
				return
			}
			if !errors.Is(err, ErrContentMismatch) {
				t.Fatalf("err = %v, want ErrContentMismatch", err)
			}
			var cm *ContentMismatchError
			if !errors.As(err, &cm) || cm.Field != tc.mismatch {
				t.Fatalf("mismatch = %+v, want field %q", cm, tc.mismatch)
			}
			if changed || ex.querySQL != "" {
				t.Fatal("a refused promote still ran the UPDATE")
			}
		})
	}
}
