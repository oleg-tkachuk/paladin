package adapters

import (
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
)

// With the three gates in one function they are pure, so the whole policy
// fits in a table instead of needing a Postgres per case. The integration
// test still drives all three repos end to end; this pins the decision itself,
// including the combinations a fixture would have to work to produce.
func TestBucketOpAllowed(t *testing.T) {
	cases := []struct {
		name           string
		write          bool
		enabled        bool
		readOnly       bool
		provisionState string
		want           error
	}{
		{"read on a healthy bucket", false, true, false, "ready", nil},
		{"write on a healthy bucket", true, true, false, "ready", nil},

		// Disabled refuses both classes: the backend is out of service, and
		// a read would resolve to a bucket nobody is maintaining.
		{"read on a disabled backend", false, false, false, "ready", object.ErrBackendDisabled},
		{"write on a disabled backend", true, false, false, "ready", object.ErrBackendDisabled},

		// Drain refuses mutations only — serving reads is the point of it.
		{"read while draining", false, true, true, "ready", nil},
		{"write while draining", true, true, true, "ready", object.ErrBackendReadOnly},

		// Same split for a bucket that does not exist on the backend yet.
		{"read while provisioning", false, true, false, "pending", nil},
		{"write while provisioning", true, true, false, "pending", object.ErrBucketProvisioning},
		{"write while deleting", true, true, false, "deleting", object.ErrBucketProvisioning},
		{"write on a failed provision", true, true, false, "failed", object.ErrBucketProvisioning},

		// Precedence, where the states overlap. Disabled outranks the rest:
		// telling an operator a bucket is still provisioning, when the
		// backend it lives on is switched off, sends them to the wrong page.
		{"disabled and draining", true, false, true, "ready", object.ErrBackendDisabled},
		{"disabled and provisioning", true, false, false, "pending", object.ErrBackendDisabled},
		{"draining and provisioning", true, true, true, "pending", object.ErrBackendReadOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bucketOpAllowed(tc.write, tc.enabled, tc.readOnly, tc.provisionState)
			if !errors.Is(got, tc.want) {
				t.Errorf("bucketOpAllowed(write=%v, enabled=%v, readOnly=%v, %q) = %v, want %v",
					tc.write, tc.enabled, tc.readOnly, tc.provisionState, got, tc.want)
			}
		})
	}
}

// Zero is an absence, not a page size: the shims forward an unset page
// message as a zero, so returning it unchanged would mean LIMIT 0 — an empty
// page from a table with rows in it.
func TestPageSizeOrDefault(t *testing.T) {
	cases := map[string]struct {
		in   int32
		want int32
	}{
		"unset":          {0, defaultPageSize},
		"negative":       {-1, defaultPageSize},
		"one":            {1, 1},
		"ordinary":       {25, 25},
		"at the maximum": {maxPageSize, maxPageSize},
		"over":           {maxPageSize + 1, defaultPageSize},
		"absurd":         {1 << 30, defaultPageSize},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := pageSizeOrDefault(tc.in); got != tc.want {
				t.Errorf("pageSizeOrDefault(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
