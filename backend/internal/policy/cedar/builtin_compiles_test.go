package cedar

import "testing"

// The built-in layer is the base case of the whole policy hierarchy: a broken
// collection policy is caught by the tenant layer above it, a broken tenant
// policy by the built-in, and the built-in by nothing. It is 177 lines of
// Cedar in a Go constant, and until this test nothing checked that it parses.
//
// A typo in it does not fail the build or any test — it fails the first
// authorization decision the process ever makes, in production, taking the
// whole cluster's authorization with it. The bootstrap admin included, since
// the unconditional platform.admin permit lives here.
func TestBuiltinPolicyCompiles(t *testing.T) {
	if _, err := compile(""); err != nil {
		t.Fatalf("the built-in policy does not compile: %v\n\n"+
			"Every authorization decision in the product is evaluated against it, so this "+
			"is a total outage the moment the binary starts serving.", err)
	}
}

// And it must still compile with a tenant policy appended, which is how it is
// actually used — a mistake in the join (a missing newline, a stray delimiter)
// would show up here rather than at the first request.
func TestBuiltinPolicyCompilesWithATenantLayer(t *testing.T) {
	if _, err := compile(`permit(principal, action, resource);`); err != nil {
		t.Fatalf("built-in + tenant layer does not compile: %v", err)
	}
}
