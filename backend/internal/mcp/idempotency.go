package mcp

import "github.com/oleg-tkachuk/paladin/internal/rpcmeta"

// wantsIdempotencyKey asks the contract instead of the method name.
//
// This was a prefix list — `Create`, `Issue`, `Delegate`, `Grant`, `Upload`,
// `Complete`, `Initiate`, `Copy`, `Restore`, `Batch` — duplicated verbatim in
// cmd/seed-fixture and, in TypeScript, in the console's transport, each with a
// comment telling the reader to change the other two.
//
// The prefixes were not merely duplicated, they were wrong in both directions.
// `Grant` swept in GrantScopes, which is a set union and needs no key.
// `Restore` swept in RestoreObjectVersion, which is OCC-guarded. And nothing in
// the list covered ChangePassword, MigrateTenantStorageLayout or PresignPart,
// each of which creates state over a connection that can drop.
func wantsIdempotencyKey(procedure string) bool {
	return rpcmeta.NeedsIdempotencyKey(procedure)
}
