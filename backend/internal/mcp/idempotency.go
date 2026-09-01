package mcp

import "strings"

// wantsIdempotencyKey names the RPCs whose replay is meaningful.
//
// Mirrors frontend/src/lib/connect/transport.ts and
// backend/cmd/seed-fixture/main.go. Three copies in two languages is one too
// many; the classification wants to live in the proto as `idempotency_level`,
// which is recorded in BACKLOG. Until then: change one, change all three.
//
// Excluded on purpose — each is already collapsed by other means, so a key
// buys a row in idempotency_keys and no safety:
//   - reads (and memoizing one would serve a stale response);
//   - Update*/Set*, guarded by resource_version;
//   - Delete*, idempotent by nature;
//   - credential minting, which the server refuses to memoize at all
//     (middleware.CredentialMintingProcedures) because a replayed refresh
//     token is one the server has already rotated away.
func wantsIdempotencyKey(procedure string) bool {
	idx := strings.LastIndex(procedure, "/")
	if idx < 0 || idx == len(procedure)-1 {
		return false
	}
	method := procedure[idx+1:]
	for _, p := range []string{
		"Create", "Issue", "Delegate", "Grant",
		"Upload", "Complete", "Initiate", "Copy", "Restore", "Batch",
	} {
		if strings.HasPrefix(method, p) {
			return true
		}
	}
	return false
}
