package admin

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
)

// The guard exists because its absence was reachable from a button. Anything
// the engine cannot compile must be refused with a code the caller can act on,
// and an empty policy must stay writable — clearing a policy is not the same
// as writing one that fails to parse.
func TestRequireCompilablePolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy string
		want   connect.Code
	}{
		{"empty clears the policy", "", 0},
		{"a well-formed permit", `permit(principal, action, resource);`, 0},
		{"a well-formed forbid", `forbid(principal, action, resource);`, 0},
		{"permit missing its clauses", "permit(principal);", connect.CodeInvalidArgument},
		{"unterminated", "permit(principal, action, resource)", connect.CodeInvalidArgument},
		{"prose, not policy", "everyone can read everything", connect.CodeInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := requireCompilablePolicy(tc.policy)
			if tc.want == 0 {
				if err != nil {
					t.Fatalf("rejected a policy that compiles: %v", err)
				}
				return
			}
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("code = %v, want %v (err=%v)", connect.CodeOf(err), tc.want, err)
			}
			// The parser's message is the half the operator needs; a bare
			// "invalid policy" would send them back to guessing.
			if !strings.Contains(err.Error(), "does not compile") {
				t.Errorf("error text %q does not say what went wrong", err.Error())
			}
		})
	}
}
