# ADR-0030: AGPL for the service, Apache-2.0 for what clients embed

- **Status:** Accepted — implemented 2026-10-09.

- **Context.** The repository was Apache-2.0 throughout. That lets anyone
  run a modified Paladin as a hosted service without publishing the changes.
  A single copyleft licence over the whole tree would close that, but it would
  also reach every program that imports an SDK: the generated clients, the
  SDKs built on them and the capability module are linked into the client,
  which would become a derivative work.

- **Decision.** The repository is AGPL-3.0-only. The client surface stays
  Apache-2.0, each directory under its own `LICENSE`: `proto/` (the contract
  the SDKs are generated from), `sdk/go/`, `sdk/python/` and `capability/`.
  `NOTICE` lists the exceptions. Version 3 only, not "or later": a future
  licence version applies only by an explicit decision.

- **Consequences.**
  - A modified backend or console offered over a network must offer its
    source to the users of that service (AGPL §13).
  - A client using the SDKs, the generated stubs or the capability module
    carries no AGPL obligation.
  - Code moving from the service into an Apache-2.0 directory changes licence
    and needs the copyright holder's consent; moving the other way is free.
  - Dependencies are unaffected: Apache-2.0, MIT and BSD code combines into
    an AGPL work, and a copyleft dependency still needs an ADR.
