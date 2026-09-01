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
		"/paladin.data.v1.ObjectService/RestoreObjectVersion",
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
		"/paladin.iam.v1.AuthService/RefreshToken",
	} {
		if wantsIdempotencyKey(proc) {
			t.Errorf("%s should not carry an Idempotency-Key", proc)
		}
	}
}
