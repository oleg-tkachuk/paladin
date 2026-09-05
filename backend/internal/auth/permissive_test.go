package auth

import "testing"

// allowsMissing decides which procedures may be called with NO Authorization
// header. It is the allow-list for unauthenticated access and it had no test.
//
// Mutation testing found that: flipping `i >= 0 && procedure[i+1:] == allowed`
// to `||` makes every procedure containing a slash — which is all of them —
// allowed without a credential, and `go test ./internal/auth/` stayed green.
// A complete authentication bypass with nothing to say so.
func TestAllowsMissingMatchesOnlyWhatItShould(t *testing.T) {
	p := &PermissiveInterceptor{
		AllowMissingFor: []string{"Login", "/paladin.iam.v1.AuthService/RefreshToken"},
	}

	allowed := []string{
		// Bare method name, matched against the last path segment.
		"/paladin.iam.v1.AuthService/Login",
		// Full procedure, matched whole.
		"/paladin.iam.v1.AuthService/RefreshToken",
	}
	for _, proc := range allowed {
		if !p.allowsMissing(proc) {
			t.Errorf("%s is on the list and was refused — the endpoints that "+
				"MUST work without a credential are the ones a caller uses to "+
				"get one", proc)
		}
	}

	denied := []string{
		// The case the mutation opened: a procedure that is not on the list at
		// all, and every one of them contains a slash.
		"/paladin.admin.v1.TenantService/DeleteTenant",
		"/paladin.data.v1.ObjectService/UploadObject",
		// A method whose name merely CONTAINS an allowed one.
		"/paladin.iam.v1.AuthService/LoginAsSomeoneElse",
		// The allowed suffix in the wrong position.
		"/paladin.iam.v1.Login/DeleteEverything",
		// No slash at all.
		"Login-ish",
		"",
	}
	for _, proc := range denied {
		if p.allowsMissing(proc) {
			t.Errorf("%s may be called with no credential — it is not on the "+
				"allow-list", proc)
		}
	}
}

// An empty list allows nothing. The zero value of this interceptor must be the
// closed one: a misconfiguration that produces no list should lock the door,
// not open it.
func TestEmptyAllowListAllowsNothing(t *testing.T) {
	p := &PermissiveInterceptor{}
	for _, proc := range []string{
		"/paladin.iam.v1.AuthService/Login",
		"/paladin.admin.v1.TenantService/DeleteTenant",
	} {
		if p.allowsMissing(proc) {
			t.Errorf("%s allowed with an empty allow-list", proc)
		}
	}
}
