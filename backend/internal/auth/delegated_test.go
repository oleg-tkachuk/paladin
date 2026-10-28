package auth

import (
	"slices"
	"testing"

	"github.com/oleg-tkachuk/limes"
)

// What a user consents to is what the capability the MCP server exchanges
// for may do, and the plane reads the same consent back from it.
func TestDelegatedOpsFollowConsent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scopes    []string
		mayWrite  bool
		wantWrite []limes.Op
	}{
		{"read only", []string{OAuthScopeRead}, false, nil},
		{"read and write", []string{OAuthScopeRead, OAuthScopeWrite}, true, []limes.Op{limes.OpPut, limes.OpDelete, limes.OpManage, OpTag, OpDelegatedWrite}},
		{"write alone still reads", []string{OAuthScopeWrite}, true, []limes.Op{limes.OpPut}},
		{"an unknown scope beside read", []string{OAuthScopeRead, "openid"}, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops, err := DelegatedOps(tc.scopes)
			if err != nil {
				t.Fatalf("DelegatedOps: %v", err)
			}
			for _, op := range []limes.Op{limes.OpGet, limes.OpList, OpPresign, OpDelegatedRead} {
				if !slices.Contains(ops, op) {
					t.Errorf("ops %v lack the read %q", ops, op)
				}
			}
			for _, op := range tc.wantWrite {
				if !slices.Contains(ops, op) {
					t.Errorf("ops %v lack the write %q", ops, op)
				}
			}
			if !tc.mayWrite && slices.ContainsFunc(ops, func(op limes.Op) bool {
				return slices.Contains(delegatedWriteOps, op)
			}) {
				t.Errorf("a read-only consent got a write: %v", ops)
			}
			if slices.Contains(ops, limes.OpShare) {
				t.Errorf("ops %v can be delegated further", ops)
			}

			back := DelegatedScopes(ops)
			p := &Principal{OAuthClientID: "agent", OAuthScopes: back}
			if p.MayWrite() != tc.mayWrite {
				t.Errorf("scopes read back %v: may write %v, want %v", back, p.MayWrite(), tc.mayWrite)
			}
		})
	}
}

// A grant that consents to neither scope is refused, not read as read-only.
func TestDelegatedOpsRefuseNoConsent(t *testing.T) {
	for _, scopes := range [][]string{nil, {"openid", "profile"}} {
		if ops, err := DelegatedOps(scopes); err == nil {
			t.Errorf("DelegatedOps(%v) = %v, want a refusal", scopes, ops)
		}
	}
}
