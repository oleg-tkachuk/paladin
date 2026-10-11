# ADR-0031: A capability is verified in the interceptor and checked in the handler

- **Status:** Accepted — 2026-10-11.
- **Related:** [ADR-0029](0029-capability-outside-the-request-path.md).

- **Context.** limes v0.4.0 added `StandardVerifier.Authorize`, which verifies
  a capability and checks every stateless caveat against an operation and a
  resource in one call. Paladin verifies in the capability interceptor
  (`Verify`) and checks the caveats in each handler (`AssertCapabilityOp`).
  `Authorize` needs the operation, the resource and whether the object is
  tainted at verify time. The interceptor knows none of them: the resource is
  in the request message, spelled per RPC, and the taint flag is on the object
  the handler reads from the database.

- **Decision.** Paladin keeps `Verify` in the interceptor and the caveat
  check in the handler, and does not call `Authorize`. Moving the check would
  need a table of every RPC's operation and resource, and a second read of
  each object for its taint flag, to enforce exactly what the handlers enforce
  now.

- **Consequences.** A handler a capability can reach must assert its
  operation; `backend/tests/contract/capability_op_asserted_test.go` fails on
  a data-plane RPC whose handler does not, and `capability_planes_test.go`
  fails if a capability reaches admin or iam on its own. The stateless caveats
  checked before the handler — the source network, the request count — stay
  in the interceptor.
