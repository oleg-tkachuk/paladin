package adapters

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

func TestIsErrorOf(t *testing.T) {
	ok, failed := admindomain.AuditOutcomeOK, admindomain.AuditOutcomeError
	for name, tc := range map[string]struct {
		outcomes []admindomain.AuditOutcome
		want     *bool
	}{
		"neither is any": {nil, nil},
		"both is any":    {[]admindomain.AuditOutcome{ok, failed}, nil},
		"ok":             {[]admindomain.AuditOutcome{ok}, new(false)},
		"error":          {[]admindomain.AuditOutcome{failed}, new(true)},
	} {
		got := isErrorOf(tc.outcomes)
		if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
			t.Errorf("%s: isErrorOf(%v) = %v, want %v", name, tc.outcomes, got, tc.want)
		}
	}
	if nilIfNone([]string{}) != nil || nilIfNone(nil) != nil || len(nilIfNone([]string{"x"})) != 1 {
		t.Error("nilIfNone narrows by nothing only when there is nothing")
	}
}
