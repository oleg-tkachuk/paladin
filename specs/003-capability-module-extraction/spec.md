# Feature Specification: Capability Module Extraction

**Feature Branch**: `003-capability-module-extraction`

**Created**: 2026-07-23

**Status**: Implemented

**Input**: User description: "Extract the capability authorisation primitive (internal/capability) from the Paladin monorepo into a standalone, independently-consumable Go module. The module must be importable by third parties who do not use object storage at all, with Paladin remaining the reference implementation that consumes it."

## Context

Paladin's capability primitive is a short-lived, signed, delegable, individually
revocable authorisation token carrying **budget and lifetime caveats** and
per-call attribution — designed for agentic workloads where an orchestrator
issues strictly-narrower sub-tokens to the agents it spawns.

That primitive has no conceptual dependency on object storage. A code survey
confirms the coupling is already near-nil: the package imports **zero** Paladin
internal packages. Its value is currently unreachable to anyone who does not
want an S3 control plane, which is the problem this feature solves.

The extraction is therefore a **packaging and boundary** exercise, not a
rewrite. The one real leak is that the usage/metering contract names a
database-specific transaction handle, which forces a database driver
dependency onto every consumer.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Third-party integrator adopts the primitive (Priority: P1)

A developer building an agent platform — with no object storage anywhere in
their stack — wants budgeted, delegable, revocable tokens for their agents.
They add the module as a dependency, supply their own storage for capability
records, and issue/verify/delegate tokens. They never pull in object-storage
code, an S3 SDK, or a specific database driver.

**Why this priority**: This is the entire point of the extraction. If a
consumer who does not use object storage cannot adopt the module cleanly, the
feature has failed regardless of what else works.

**Independent Test**: Build a minimal consumer program in a separate module
that depends only on the new module, implements the storage contracts
in-memory, and completes an issue → verify → delegate → revoke cycle. Inspect
its resolved dependency graph for absence of storage/database packages.

**Acceptance Scenarios**:

1. **Given** a fresh module that depends only on the capability module,
   **When** the developer issues a token, verifies it, delegates a narrower
   child, and revokes the parent, **Then** all four operations succeed using
   only contracts the module itself publishes.
2. **Given** that same consumer, **When** its full dependency graph is
   resolved, **Then** it contains no object-storage packages and no database
   driver.
3. **Given** a consumer that supplies an in-memory implementation of the
   storage contracts, **When** it exercises budget exhaustion, expiry, and
   revocation, **Then** each rejection is reported as a distinct, matchable
   error condition rather than a generic failure.

---

### User Story 2 - Paladin keeps working unchanged (Priority: P1)

Paladin continues to issue, verify, delegate, revoke, and meter capabilities
exactly as before, now consuming the extracted module instead of an internal
package. Operators observe no behavioural change: the same tokens verify, the
same budgets enforce, the same revocations propagate, the same metrics and
audit records appear.

**Why this priority**: Equal-highest with US1. An extraction that regresses
the reference implementation is not a success — Paladin is both the proof the
module works and the thing currently in production use.

**Independent Test**: Run Paladin's existing capability, authorisation,
budget/charge, and outbox test suites unchanged against the extracted module
and confirm they pass without modification to their assertions.

**Acceptance Scenarios**:

1. **Given** Paladin built against the extracted module, **When** the existing
   automated suites run, **Then** every capability, authorisation, billing,
   and transactional-outbox test passes with no assertion changed.
2. **Given** a capability token issued by the pre-extraction build, **When**
   it is presented to the post-extraction build, **Then** it verifies
   successfully — the token wire format is unchanged.
3. **Given** a charge against a capability, **When** it commits, **Then** the
   spend counter, the ledger record, and the event fan-out still commit
   atomically, with no partial-write window introduced.
4. **Given** a capability is revoked, **When** an in-flight caller presents
   it, **Then** it is rejected within the same propagation window as before.

---

### User Story 3 - Reference storage implementation stays with Paladin (Priority: P2)

An operator running Paladin gets a production-grade persistent implementation of
the capability storage contracts out of the box. A third party who wants the
same persistence can read that implementation as a worked example rather than
being forced to adopt it.

**Why this priority**: Valuable but not blocking. The module is usable with a
consumer-supplied store; shipping the persistent one is what makes Paladin a
credible reference rather than a toy.

**Independent Test**: Confirm Paladin's persistent store satisfies the module's
published contracts and that the module's own test suite passes without it.

**Acceptance Scenarios**:

1. **Given** the extracted module, **When** its test suite runs in isolation,
   **Then** it passes without any persistent storage available.
2. **Given** Paladin, **When** it starts, **Then** it supplies its persistent
   store to the module through the published contracts only.

---

### User Story 4 - Independent release and adoption signal (Priority: P3)

A prospective adopter can discover the module, read a focused document that
explains the primitive on its own terms, see a runnable example, and pin a
specific released version without tracking Paladin's release cadence.

**Why this priority**: Adoption enablement. The extraction is technically
complete without it, but unreleased and undocumented code gets no adopters,
which defeats the strategic purpose.

**Independent Test**: From a clean environment, follow only the module's own
documentation to reach a working issue/verify cycle.

**Acceptance Scenarios**:

1. **Given** only the module's documentation, **When** a developer follows it
   end to end, **Then** they reach a working issue → verify cycle without
   consulting Paladin's documentation.
2. **Given** a published version, **When** a consumer pins it, **Then**
   subsequent Paladin changes do not alter that consumer's resolved dependency.

---

### Edge Cases

- **Cross-currency delegation**: a child token declaring a different budget
  unit than its parent must be rejected at issuance rather than silently
  converted.
- **Widening delegation**: a child requesting operations, resources, budget,
  or lifetime exceeding its parent must be rejected.
- **Signing key rotation**: a token signed under a retired key must still
  verify while that key remains published, and stop verifying once withdrawn.
- **Revocation during an in-flight operation**: a capability revoked mid-call
  must not allow a subsequent write to land under the stale decision.
- **Missing capability record**: a syntactically valid token whose record is
  absent must be treated as forgery, not as a missing entity.
- **Budget exhaustion mid-charge**: a charge that would exceed either the
  per-capability or the tenant-level ceiling must leave *both* counters
  unmutated.
- **Unset budget unit**: a record written before units were tracked must
  resolve to the documented default rather than an empty unit.
- **Consumer without transactional storage**: a consumer whose store cannot
  offer atomic multi-write must still be able to use issue/verify/delegate/
  revoke, with the atomicity guarantee documented as store-dependent.

## Requirements *(mandatory)*

### Functional Requirements

#### Module boundary

- **FR-001**: The capability primitive MUST be consumable as an independently
  versioned unit, resolvable without depending on Paladin.
- **FR-002**: The module MUST NOT require any consumer to take on an
  object-storage dependency.
- **FR-003**: The module MUST NOT require any consumer to take on a specific
  database or database-driver dependency.
- **FR-004**: The module MUST publish, as part of its own contract, every
  extension point a consumer has to implement: capability record storage,
  usage/budget accounting, and signing-key resolution.
- **FR-005**: The published contracts MUST be expressible by a consumer whose
  persistence is not a relational database, including an in-memory one.

#### Behaviour preservation

- **FR-006**: The token wire format MUST be unchanged, so tokens issued before
  the extraction verify after it and vice versa.
- **FR-007**: Every error condition a caller can currently distinguish —
  invalid signature, expired, not yet valid, revoked, caveat violation,
  audience mismatch, budget exceeded, over-wide delegation, unit mismatch —
  MUST remain individually distinguishable by consumers.
- **FR-008**: Delegation MUST continue to reject any child that is wider than
  its parent along any caveat dimension.
- **FR-009**: Revocation MUST continue to propagate within the consumer's
  configured window, and MUST continue to fence writes that were decided
  against a now-stale view.
- **FR-010**: Budget enforcement MUST continue to reject at the authorisation
  boundary, before business logic executes.

#### Atomicity

- **FR-011**: The usage/accounting contract MUST allow a consumer whose store
  supports transactions to commit the spend counter, the ledger record, and
  any consumer-side side effects as one unit.
- **FR-012**: That contract MUST express the above **without naming any
  specific database technology**, so a consumer using different persistence
  can satisfy it.
- **FR-013**: When any ceiling rejects a charge, no counter may be left
  mutated.

#### Paladin as consumer

- **FR-014**: Paladin MUST consume the module through its published contracts
  only, with no privileged access unavailable to third parties.
- **FR-015**: Paladin MUST retain its persistent implementation of the storage
  contracts, and that implementation MUST NOT ship inside the module.
- **FR-016**: All existing Paladin consumers of the primitive — the request
  authorisation path, the administrative issue/revoke/list surface, the
  billing surface, the data-plane handlers, the background expiry worker, and
  configuration — MUST continue to function unchanged in observable behaviour.
- **FR-017**: Paladin's existing automated coverage of the primitive MUST continue
  to pass without assertion changes.

#### Adoption

- **FR-018**: The module MUST carry documentation that explains the primitive
  without reference to object storage.
- **FR-019**: The module MUST include at least one runnable example covering
  issue → verify → delegate → revoke against a consumer-supplied store.
- **FR-020**: The module MUST be independently versioned, so a consumer can
  pin a version unaffected by Paladin's release cadence.

### Key Entities

- **Capability**: a bearer authority record — subject, audience, validity
  window, caveats, delegation lineage, and generation marker used to fence
  stale decisions.
- **Principal**: who the capability is for — a human user, a service, or an
  agent. The agent variant additionally carries the run and parent-agent
  lineage that makes delegated calls attributable.
- **Caveats**: the narrowing constraints — permitted operations, permitted
  resources, request ceiling, budget ceiling with its unit, and assorted
  safety flags.
- **Usage**: the running counters for one capability — requests consumed and
  budget spent, in a declared unit.
- **Tenant budget**: an aggregate ceiling above individual capabilities, so a
  set of tokens cannot collectively exceed an owner's limit.
- **Revocation entry**: the record that withdraws a capability, optionally
  cascading to its descendants.
- **Signing key set**: the published verification keys, allowing verification
  without a round trip to the issuer.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A consumer that does not use object storage can complete an
  issue → verify → delegate → revoke cycle using only the module and its own
  storage implementation.
- **SC-002**: The dependency set that such a consumer inherits contains zero
  object-storage packages and zero database drivers.
- **SC-003**: 100% of Paladin's existing capability, authorisation, billing, and
  outbox tests pass after the extraction with no assertion modified.
- **SC-004**: A token issued before the extraction verifies after it, and the
  reverse, with no migration step.
- **SC-005**: All nine currently-distinguishable rejection conditions remain
  individually distinguishable by a consumer.
- **SC-006**: The module's own test suite passes with no external
  infrastructure — no database, no network, no container.
- **SC-007**: A developer reaches a working issue → verify cycle using only
  the module's own documentation, without opening Paladin's.
- **SC-008**: Charge atomicity is demonstrated: an induced failure in the
  consumer-side side effect leaves the spend counter and ledger unchanged.
- **SC-009**: Token verification on the request path shows **no more than 5%
  increase at p99** against the pre-extraction baseline, measured over at
  least 10 000 iterations of the same verification call on the same hardware.
  The threshold is stated numerically because "no measurable regression" is
  not a testable claim — a percentile, a sample size, and a bound are what
  make it one.

## Assumptions

- **Pre-1.0 breakage is acceptable.** Per the project constitution, backward
  compatibility is not required at the source level. The **token wire format**
  is nonetheless treated as fixed (FR-006), because tokens outlive deployments
  and a format break would invalidate live credentials.
- **Same repository, separate module.** The extraction is assumed to keep the
  code in this repository as a nested module rather than moving it to a
  separate repository. This preserves atomic cross-cutting changes and the
  existing CI, while still delivering independent importability and
  versioning. Splitting the repository later remains possible and is out of
  scope here.
- **The persistent store stays with Paladin.** The relational implementation is
  the reference, not part of the module's contract surface.
- **Consumers supply their own observability.** The module may emit
  instrumentation but must not require a specific collector to function.
- **No new capability features.** This is a boundary change. Behavioural
  changes, new caveat types, and the deferred key-management work in the
  backlog are explicitly out of scope.
- **Documentation-only extraction of examples.** Example consumers are
  illustrative and are not a supported product surface.

## Out of Scope

- Moving the code to a different repository or organisation.
- Any change to the token format, caveat semantics, or delegation rules.
- Externally-managed signing keys (tracked separately in the backlog).
- Publishing to a package registry or announcing the module.
- Extracting any other Paladin component.
