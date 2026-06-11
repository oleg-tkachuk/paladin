package statemachine

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestIsNoRows pins errors.Is semantics: a wrapped pgx.ErrNoRows must
// classify as no-rows (→ changed=false, no error) — the old string
// compare returned false for wrapped sentinels, which made
// PromoteToAvailable surface a phantom error and re-queue the object
// on every reconciler tick.
func TestIsNoRows(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"bare sentinel", pgx.ErrNoRows, true},
		{"wrapped once", fmt.Errorf("scan: %w", pgx.ErrNoRows), true},
		{"wrapped twice", fmt.Errorf("sm: %w", fmt.Errorf("scan: %w", pgx.ErrNoRows)), true},
		{"unrelated", errors.New("connection refused"), false},
		{"same text, different error", errors.New("no rows in result set"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNoRows(tc.err); got != tc.want {
				t.Errorf("isNoRows(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
