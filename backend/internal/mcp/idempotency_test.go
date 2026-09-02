package mcp

import "testing"

// The bridge drives the object lifecycle on behalf of an agent, and sent no
// Idempotency-Key on any of it. Nothing rejected that — none of these RPCs is
// Create*/Issue*, the only shape the server requires a key on — so the gap was
// silent: a retried upload got AlreadyExists where it should have got the
// original object back.
func TestObjectLifecycleCallsCarryAKey(t *testing.T) {
	for _, proc := range []string{
		"/paladin.data.v1.ObjectService/UploadObject",
		"/paladin.data.v1.ObjectService/CompleteObject",
		"/paladin.data.v1.ObjectService/CopyObject",
		"/paladin.data.v1.MultipartUploadService/InitiateMultipartUpload",
		"/paladin.data.v1.MultipartUploadService/CompleteMultipartUpload",
	} {
		if !wantsIdempotencyKey(proc) {
			t.Errorf("%s carries no Idempotency-Key; a retry answers AlreadyExists", proc)
		}
	}
}

// Not everything the bridge calls. Reads must not be memoized at all, and the
// rest are already collapsed by resource_version or by being deletes.
func TestReadsAndGuardedWritesCarryNoKey(t *testing.T) {
	for _, proc := range []string{
		"/paladin.data.v1.ObjectService/ListObjects",
		"/paladin.data.v1.ObjectService/GetObject",
		"/paladin.data.v1.ObjectService/CountObjects",
		"/paladin.data.v1.ObjectService/UpdateObject",
		"/paladin.data.v1.ObjectService/DeleteObject",
		"/paladin.admin.v1.BucketService/SetLifecycleRules",
		// RestoreObjectVersion was in the list ABOVE until the descriptor
		// replaced the prefix list, and moving it is the change, not a
		// regression: the old rule swept it in on the `Restore` prefix, and it
		// takes resource_version. A repeat writes the same value or fails
		// Aborted, so a key bought a row in idempotency_keys and no safety.
		"/paladin.data.v1.ObjectService/RestoreObjectVersion",
	} {
		if wantsIdempotencyKey(proc) {
			t.Errorf("%s should not carry an Idempotency-Key", proc)
		}
	}
}

// Credential minting now DOES get a key, and that is a deliberate reversal.
//
// The prefix list excluded Login/RefreshToken/ExchangeAudience/SwitchTenant,
// on the reasoning that replaying a rotated refresh token is actively wrong —
// which is true, and is enforced where it belongs: the server refuses to
// memoize them at all (middleware.CredentialMintingProcedures, asserted by
// TestCredentialMintersAreSkipped). The client duplicating that judgement is
// precisely the three-copies problem this change exists to end.
//
// An Idempotency-Key is a caller's de-duplication token, not a request to
// cache. The server decides what to do with it, and for these four it does
// nothing. The header is inert here — but the SKIP LIST is the only thing
// making it inert, which is worth knowing when reading either side.
func TestCredentialMintersAreKeyedByClientsAndSkippedByServer(t *testing.T) {
	for _, proc := range []string{
		"/paladin.iam.v1.AuthService/Login",
		"/paladin.iam.v1.AuthService/RefreshToken",
		"/paladin.iam.v1.AuthService/ExchangeAudience",
		"/paladin.iam.v1.AuthService/SwitchTenant",
	} {
		if !wantsIdempotencyKey(proc) {
			t.Errorf("%s: expected the descriptor rule to key it — if this RPC "+
				"gained an idempotency_level, the skip list is now the only "+
				"documentation of why replaying it is wrong", proc)
		}
	}
}
