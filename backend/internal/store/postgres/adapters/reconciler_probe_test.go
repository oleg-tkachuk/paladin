package adapters

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
)

// isNotFoundErr decides whether ReconcilerV2 takes the terminal MarkFailed
// branch. It shipped as `return false`, which made that branch unreachable —
// so the object whose bytes were genuinely gone was retried every 30s
// forever, logging a HEAD warning each time.
//
// Nothing tested the stub, which is why it survived. These cover both
// directions, with the emphasis on the one that costs something: a false
// positive marks a live object FAILED.
func TestIsNotFoundErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "sentinel from the storage adapter",
			err:  fmt.Errorf("head: %w: %w", s3adapter.ErrObjectNotFound, errors.New("404")),
			want: true,
		},
		{
			name: "sentinel wrapped again by a caller",
			err:  fmt.Errorf("probe: %w", fmt.Errorf("head: %w", s3adapter.ErrObjectNotFound)),
			want: true,
		},
		{
			name: "bare sentinel",
			err:  s3adapter.ErrObjectNotFound,
			want: true,
		},
		// Everything below is a backend that could not answer, not a backend
		// that answered "absent". All must stay transient.
		{"nil", nil, false},
		{"timeout", context.DeadlineExceeded, false},
		{"cancelled", context.Canceled, false},
		{"connection refused", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, false},
		{"access denied", errors.New("head: operation error S3: HeadObject, AccessDenied"), false},
		{"server error", errors.New("head: operation error S3: HeadObject, 500 InternalError"), false},
		{
			// The trap: an error whose *text* says not found but which carries
			// no sentinel. Matching on strings is how this check gets
			// reintroduced wrongly, so pin that it does not.
			name: "text says NotFound but carries no sentinel",
			err:  errors.New("head: operation error S3: HeadObject, 404 NotFound"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNotFoundErr(tc.err); got != tc.want {
				t.Errorf("isNotFoundErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
