## Unreleased — lease and Delegate refusals name the presented capability

Codes and reasons are unchanged; only the message text moved. A client that
matches on the text should match on the code and the reason instead.

- `CapabilityService.Lease`, `GetLease` and `SettleLease`, refused for the
  capability they present, no longer begin with `capability:`:
  - `capability: a Biscuit copy cannot lease; …` is `the presented capability
    is a Biscuit copy, which cannot lease; present the capability it was
    attenuated from`;
  - `capability: leasing for a capability requires OpShare` is `the presented
    capability does not allow share, which leasing for a capability requires`;
  - `capability: a capability leases only for itself or …` is `the presented
    capability leases only for itself or a capability delegated from it`;
  - `capability: the store names no ancestors, …` is `the capability store
    names no ancestors, so a lease for another capability cannot be checked`.
- `CapabilityService.Delegate`:
  - `capability: delegation narrows a presented capability; send it in the
    X-Paladin-Capability header` loses its prefix;
  - `capability: parent_id must equal caller's own capability id` is
    `parent_id must be the id of the presented capability`;
  - `capability: a Biscuit copy cannot delegate; attenuate it instead` is `the
    presented capability is a Biscuit copy, which cannot delegate; attenuate
    it instead`.
- Both SDKs' fakes (`paladintest`, `paladin.testing`) answer with the same
  texts. Their Delegate from a parent without `share` now answers as the
  server does — `PERMISSION_DENIED`, `The token does not allow this request`,
  with reason `ERROR_REASON_CAPABILITY_OP_NOT_ALLOWED` and limes code
  `op_not_allowed` — where it said `capability: caller lacks OpShare`, and
  checks `parent_id` and a Biscuit copy before `share`, in the server's order.
