// Package capability is an object-capability authorisation primitive designed
// for agentic workloads.
//
// A Capability is a short-lived, signed token granting its bearer a specific
// set of operations on a specific set of resources, with explicit budget and
// lifetime caveats. It is delegable — a parent issues a strictly narrower
// child to a sub-agent — and individually revocable. Compared with long-lived
// API keys or role-based access it gives:
//
//   - Per-call attribution: every call carries the capability ID, so audit and
//     cost roll up by it, down to which run of which agent spent what.
//   - Budget enforcement at the authorisation boundary: the verifier rejects
//     once the ceiling is exhausted, rather than business logic discovering it
//     three calls deep.
//   - Mid-flight revocation: a compromised agent's capability is revoked
//     atomically, optionally cascading to everything it delegated.
//   - Sub-capabilities without a round trip: an orchestrator narrows its own
//     authority for each sub-agent it spawns, with no admin API in the path.
//
// Wire format is JWT compact serialization with Ed25519 signatures (EdDSA).
// Verification is local: the signature is checked against a resolved public
// key, so consumers scale horizontally without contending on a shared store.
//
// # What you implement
//
// The module publishes three contracts and nothing more. A consumer that
// implements them needs nothing else:
//
//   - [Store] — capability records and revocations. No transactions required;
//     an in-memory map is a valid implementation.
//   - [UsageStore] — request and spend counters. Generic in TX, the consumer's
//     transaction handle; see "No-transaction mode" below.
//   - [KeyResolver] — public verification keys. [StaticKeyResolver] ships as a
//     working implementation and supports rotation via SetKey.
//
// Store and UsageStore are deliberately separate types: both declare a method
// named Get with different signatures, so no single type can satisfy both.
// Any implementer will hit this; the memstore subpackage shows the split.
//
// # No-transaction mode
//
// UsageStore.Charge takes an onCharged callback that runs inside the charge,
// receiving the consumer's transaction handle, so a side effect such as an
// outbox write commits atomically with the spend. The module never constructs,
// inspects or constrains that handle — it only threads it back. That is what
// keeps this package free of any database dependency.
//
// A consumer with no transactional storage instantiates UsageStore[struct{}]
// and always passes nil for onCharged. Everything else behaves identically;
// only the atomic-side-effect guarantee is forgone, and that limitation is
// store-dependent rather than a degradation of the primitive.
//
// # Guarantees
//
// Rejections are individually matchable sentinels, so a consumer can map each
// to its own transport status; the module does not choose status codes. A
// charge rejected by either the capability or the tenant ceiling leaves both
// counters and the ledger unmutated, so retrying after a rejection is safe.
// Delegation can only narrow: any widening returns [ErrDelegationTooWide], and
// a differing unit code returns [ErrUnitCodeMismatch] rather than converting.
//
// See README.md for a five-minute walkthrough and example/ for it as a
// runnable program.
package capability
