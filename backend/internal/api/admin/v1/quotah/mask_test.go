package quotah

import (
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

func capOf(n int64) *int64 { return &n }

func TestApplyCapMask(t *testing.T) {
	const (
		storedBytes  = 100
		storedCount  = 7
		requestBytes = 5
	)
	current := admindomain.Quota{MaxTotalBytes: capOf(storedBytes), MaxObjectCount: capOf(storedCount)}
	for name, tc := range map[string]struct {
		req   admindomain.Quota
		mask  []string
		bytes *int64
		count *int64
	}{
		"set one, keep the other": {
			req:  admindomain.Quota{MaxTotalBytes: capOf(requestBytes), MaxObjectCount: nil},
			mask: []string{CapMaxTotalBytes}, bytes: capOf(requestBytes), count: capOf(storedCount),
		},
		"a named cap left nil is lifted": {
			req: admindomain.Quota{}, mask: []string{CapMaxObjectCount}, bytes: capOf(storedBytes), count: nil,
		},
		"zero is a cap": {
			req: admindomain.Quota{MaxTotalBytes: capOf(0)}, mask: []string{CapMaxTotalBytes}, bytes: capOf(0), count: capOf(storedCount),
		},
	} {
		got, err := applyCapMask(current, tc.req, tc.mask)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !sameCap(got.MaxTotalBytes, tc.bytes) || !sameCap(got.MaxObjectCount, tc.count) {
			t.Errorf("%s: caps = %v / %v, want %v / %v", name,
				deref(got.MaxTotalBytes), deref(got.MaxObjectCount), deref(tc.bytes), deref(tc.count))
		}
	}
	if *current.MaxTotalBytes != storedBytes {
		t.Error("applyCapMask wrote through to current")
	}
}

func TestApplyCapMaskRefusesABadMask(t *testing.T) {
	for name, mask := range map[string][]string{
		"empty":          nil,
		"not a cap":      {"usage"},
		"one bad of two": {CapMaxTotalBytes, "name"},
	} {
		if _, err := applyCapMask(admindomain.Quota{}, admindomain.Quota{}, mask); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}
}

func sameCap(a, b *int64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func deref(p *int64) any {
	if p == nil {
		return "none"
	}
	return *p
}
