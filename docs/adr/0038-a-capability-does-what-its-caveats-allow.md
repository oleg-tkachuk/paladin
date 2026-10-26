# ADR-0038: A capability does what its caveats allow, without a tenant policy

- **Status:** Accepted. Implemented 2026-10-26.
- **Related:** amends [ADR-0010](0010-capability-as-establishing-credential.md) (a capability carries
  no role); follows the machine delete built-in and
  [ADR-0031](0031-capability-verify-then-handler.md) (the verifier in the
  interceptor, the operation check in the handler).

- **Context.** A capability names what it may do in its caveats: the
  operations, the resource prefixes and URIs, the request and budget
  ceilings, whether it may read tainted objects. `auth.AssertCapabilityOp`
  checks a request against them in the handler, before the handler asks
  Cedar. Cedar then wanted a policy that permits the action, and the
  built-in one permits by role. A capability has none by design, so a
  tenant with no stored policy refused every read and upload a capability
  was issued for, with "denied by policy". The only built-in grant a
  capability met was the machine delete permit. On the local cluster a
  capability charge could land only after a Cedar policy was written for
  the collection by hand.

- **Decision.**
  1. **The operation reaches Cedar.** `AssertCapabilityOp` already stamps
     the operation it admitted into a per-request holder, for the charges
     ledger. The holder moves to `internal/capop`, which both `auth` and
     the policy engine import, and the engine exposes it as
     `context.capability_op`. It is read from the request, never from the
     caller's `RequestContext`, so a handler cannot claim an operation
     nobody checked. A request without a capability carries `""`.
  2. **Built-in permits bind each operation to its actions.** For a
     principal of kind `capability`, in its own tenant:
     - `get` or `list` admitted → `GetObject`, `PresignGet`;
     - `put` admitted → `PutObject`, `PresignPut`, `UpdateObject`,
       `CopyObject`;
     - `object:tag` admitted → `UpdateObject` (the batch tag change);
     - `delete` → the machine delete permit already covers
       `DeleteObject` and `RestoreObject`.
     `object:presign` is asserted only beside `get` or `put`, which grant
     the action; `share` opens `Delegate`, which asks no policy.
     A read admitted on one object cannot authorize an upload, and a
     request whose handler asserted nothing authorizes nothing.
  3. **`manage` gets no default.** Taint, Object Lock and operation
     cancellation stay with a tenant policy that names them.
  4. **The API serves the table.** `CapabilityService.ListOperationGrants`
     returns it with each operation's basis, from
     `cedar.CapabilityOperationGrants`; a test holds the table to the policy
     text, so the console never describes a grant the engine does not make.
  5. **A tenant can still say no.** These are permits; a tenant `forbid`
     wins, as with every built-in.

- **Consequences.**
  - A capability works on a fresh tenant: its caveats, not a policy written
    alongside it, decide what it may do. The caveats were already the
    narrower bound; Cedar no longer has to repeat them.
  - Default-deny for capabilities is gone. A tenant that relied on it to
    keep issued capabilities inert must add a `forbid`.
  - A handler that asserts one operation and then authorizes an action of
    another fails closed. A handler that authorizes before it asserts also
    fails closed; every data-plane handler today asserts first.
  - The holder keeps the last operation asserted in a request. A handler
    that asserts twice (presign, then get or put) authorizes under the last.
