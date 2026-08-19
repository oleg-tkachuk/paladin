# Feature Specification: Enable / Disable Storage Backends

**Feature Branch**: `002-backend-enable-disable`

**Created**: 2026-05-28

**Status**: Draft

**Input**: User description: "Enable/disable storage backends — a disabled backend processes no Paladin requests of any kind, is shown as disabled in the UI, and all other operations against it are inactive in the UI. Single `SetBackendEnabled` RPC (OCC-guarded), strict reject semantics, default-backend guard, reversible."

## Clarifications

### Session 2026-05-28

- Q: When a backend is disabled, which operations are rejected? → A: **All of them (strict).** A disabled backend rejects every Paladin-plane request that resolves to it — object upload/download/head/copy, presigned URL issuance (GET and PUT), multipart, and bucket creation. Reads are not exempt. Rationale: "disabled" must be an unambiguous, auditable boundary; a half-disabled backend that still serves reads invites confusion about whether the off-switch actually worked. A read-only "drain" mode can be added later as a distinct state if a migration use-case demands it.
- Q: How is the state change exposed as an operation? → A: A single new admin RPC `SetBackendEnabled(backend_id, enabled, resource_version)`. It is naturally idempotent (setting `enabled=false` on an already-disabled backend is a no-op success), guarded by optimistic concurrency (`resource_version`), and authorized by the existing `ManageBackend` permission (platform-admin). It is a state mutation, not a creation, so it does not require an idempotency key.
- Q: Are there backends that must NOT be disable-able? → A: **The configured default backend.** Disabling the backend that is the platform's default would break creation of any new bucket that does not name an explicit backend, so the system refuses to disable it. A backend that merely *has* existing buckets CAN be disabled — stopping access to those buckets is the entire point of the feature.
- Q: Does disabling a backend revoke already-issued presigned URLs? → A: **No, and this is a documented limitation.** Presigned URLs are honoured directly by the underlying object store, bypassing Paladin, so Paladin cannot retroactively revoke them. They expire on their own short time-to-live. The guarantee is precise: disabling stops Paladin from issuing *new* presigned URLs and rejects all Paladin-mediated operations; URLs already handed out remain valid until they expire.
- Q: What happens to the disabled state across a platform restart? → A: **It persists.** The platform mirrors its static backend configuration into its control-plane store at startup. That mirroring MUST preserve the operator-set enabled/disabled state — a backend an operator disabled stays disabled after a restart; it does not silently re-enable.

## User Scenarios & Testing *(mandatory)*

The stories below are ordered by operator-journey criticality. US1 alone
delivers the core safety guarantee (a disabled backend genuinely stops
serving) and is independently shippable. Later stories layer the reverse
transition, the operator-facing surface, and the guard rails.

### User Story 1 — Disabling a backend stops all access (Priority: P1)

A platform operator disables a storage backend. From that moment, every
operation Paladin performs against that backend — uploading an object,
downloading one, listing, requesting a presigned link, starting a
multipart upload, or creating a bucket on it — is refused with a clear
"backend is disabled" error, before Paladin ever contacts the object store.

**Why this priority**: This is the entire safety contract of the
feature. Without server-side enforcement, "disabled" is a cosmetic UI
label that any direct API caller can ignore — a correctness and security
hole. The reject must happen on the server, for every request path, not
just in the browser.

**Independent Test**: Disable a seeded backend, then attempt each
operation type (upload, download, presign, bucket-create) against a
bucket that resolves to it; assert every one is refused with the
documented "disabled" failure, and confirm the object store was never
contacted.

**Acceptance Scenarios**:

1. **Given** an enabled backend with at least one bucket, **When** the
   operator disables it, **Then** the operation succeeds and the backend
   is reported as disabled.
2. **Given** a disabled backend, **When** any operation that resolves to
   it is attempted (upload, download, head, copy, list, presign GET,
   presign PUT, multipart, bucket-create), **Then** the request is
   refused with a clear "backend is disabled" failure and no object-store
   call is made.
3. **Given** a disabled backend, **When** the operator disables it again
   (repeat), **Then** the call succeeds as a no-op (idempotent) and the
   backend stays disabled.

---

### User Story 2 — Re-enabling a backend restores access (Priority: P1)

An operator re-enables a previously disabled backend. Access resumes
immediately and fully: existing buckets and objects are reachable again
exactly as before — no data was lost or altered while the backend was
disabled.

**Why this priority**: Disable is only safe to use if it is trivially
reversible. An operator must be able to flip a backend off to stop a
problem and flip it back on once resolved, with zero data impact and no
migration step.

**Independent Test**: Disable a backend, confirm access is refused,
re-enable it, then repeat the same operations and assert they all
succeed against the same untouched data.

**Acceptance Scenarios**:

1. **Given** a disabled backend with existing buckets, **When** the
   operator re-enables it, **Then** subsequent operations against it
   succeed.
2. **Given** a backend that was disabled and re-enabled, **When** an
   object that existed before the disable is downloaded, **Then** it
   returns the same content — disabling never touched stored data.

---

### User Story 3 — Operator sees and controls backend state in the UI (Priority: P2)

In the admin UI, an operator can see at a glance which backends are
enabled and which are disabled, and can toggle a backend's state. A
disabled backend is clearly badged as "Disabled", is not selectable as a
working scope, and every downstream action that would operate on it
(create bucket, upload, browse) is visibly inactive rather than failing
mid-flow.

**Why this priority**: The operator-facing surface is how the feature is
actually used day to day. Surfacing state up front (badge + inactive
controls) prevents the confusing experience of starting an operation and
hitting a server rejection partway through.

**Independent Test**: With one enabled and one disabled backend seeded,
load the admin UI and the scope picker; assert the disabled backend shows
a "Disabled" badge, cannot be selected, and its downstream operations are
inactive; assert the enabled one behaves normally.

**Acceptance Scenarios**:

1. **Given** a mix of enabled and disabled backends, **When** the
   operator opens the backend list and the scope picker, **Then** each
   backend's enabled/disabled state is clearly shown and disabled ones
   carry a "Disabled" badge.
2. **Given** a disabled backend in the scope picker, **When** the
   operator views it, **Then** it is not selectable and the operations
   that depend on a selected backend are inactive.
3. **Given** the operator is viewing the backend admin surface, **When**
   they toggle a backend's state, **Then** the change takes effect and
   the displayed state updates to match.
4. **Given** a backend the operator had selected becomes disabled while
   they are working (e.g. disabled in another session), **When** they
   next attempt an operation on it, **Then** the UI shows a clear,
   non-cryptic message that the backend is disabled rather than a raw
   error.

---

### User Story 4 — Guard rails prevent unsafe disables (Priority: P2)

The system refuses to disable the platform's configured default backend,
and it safely handles two operators changing the same backend's state at
once.

**Why this priority**: The default backend is load-bearing — disabling it
would break creation of any new bucket that doesn't name an explicit
backend. And concurrent state changes must not silently clobber each
other. These guards keep the feature from becoming a foot-gun.

**Independent Test**: Attempt to disable the configured default backend
and assert it is refused with a clear reason. Then attempt a state change
with a stale concurrency token and assert it is refused as a conflict.

**Acceptance Scenarios**:

1. **Given** the backend that is the platform's configured default,
   **When** an operator attempts to disable it, **Then** the request is
   refused with a clear reason that names the default-backend constraint,
   and the backend stays enabled.
2. **Given** two operators viewing the same backend, **When** one changes
   its state and the other then submits a change based on the now-stale
   prior state, **Then** the second change is refused as a conflict so no
   silent overwrite occurs.
3. **Given** a non-default backend that has existing buckets, **When** an
   operator disables it, **Then** the disable succeeds (existing buckets
   are intentionally cut off, not a reason to block the disable).

---

### User Story 5 — Disabled state survives a platform restart (Priority: P3)

A backend an operator disabled stays disabled after the platform
restarts. The startup process that reconciles static configuration must
not silently flip a disabled backend back on.

**Why this priority**: A disable that quietly reverts on the next restart
is worse than no disable at all — it gives a false sense of safety. The
state must be durable.

**Independent Test**: Disable a backend, restart the platform, and assert
the backend is still reported as disabled and still rejects operations.

**Acceptance Scenarios**:

1. **Given** a disabled backend, **When** the platform restarts and
   reconciles its configuration, **Then** the backend remains disabled.
2. **Given** a backend that exists only in static configuration with no
   prior stored state, **When** the platform first reconciles it,
   **Then** it defaults to enabled.

---

### Edge Cases

- **Operation in flight when disable lands**: a request that already
  passed the backend-state check may complete; the guarantee is that no
  *new* request is accepted after the backend is disabled. Long-running
  transfers are not forcibly aborted by this feature.
- **Presigned URL already issued**: remains valid until its own
  expiry — disabling cannot revoke it (documented limitation). Only the
  issuance of new presigned URLs is blocked.
- **Disable then immediately re-enable**: both succeed; the backend ends
  in the last-written state, guarded by the concurrency token.
- **Default backend reassigned in config, old default disabled**: once a
  backend is no longer the configured default, the default-backend guard
  no longer protects it and it can be disabled.
- **State change with a stale concurrency token**: refused as a conflict;
  the operator must re-read current state and retry.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Each storage backend MUST carry a durable enabled/disabled
  state, defaulting to enabled for any backend that has no prior stored
  state.
- **FR-002**: The system MUST reject **every** Paladin-mediated operation that
  resolves to a disabled backend — object upload, download, head, copy,
  list, presigned-URL issuance (GET and PUT), multipart upload, and bucket
  creation — with a clear "backend is disabled" failure, and MUST do so
  before contacting the underlying object store.
- **FR-003**: Operators MUST be able to change a backend's state through a
  single operation that sets the desired enabled value. The operation MUST
  be idempotent (setting the state it already has is a successful no-op).
- **FR-004**: The state-change operation MUST be guarded by optimistic
  concurrency: a change submitted against a stale version of the backend
  MUST be refused as a conflict rather than silently overwriting a
  concurrent change.
- **FR-005**: The state-change operation MUST be restricted to operators
  holding the existing backend-management permission (platform-admin).
- **FR-006**: The system MUST refuse to disable the backend that is the
  platform's configured default, returning a clear reason naming the
  default-backend constraint.
- **FR-007**: Disabling a backend MUST NOT modify or delete any stored
  data; re-enabling MUST fully restore access to the same data.
- **FR-008**: The backend list and detail surfaces MUST expose each
  backend's enabled/disabled state.
- **FR-009**: The admin UI MUST visibly distinguish disabled backends
  (a "Disabled" badge), MUST make a disabled backend non-selectable as a
  working scope, and MUST render downstream operations on a disabled
  backend as inactive rather than allowing them to be attempted and fail.
- **FR-010**: The admin UI MUST let an authorized operator toggle a
  backend's state and reflect the new state after the change.
- **FR-011**: When an operation is refused because the backend is
  disabled, the UI MUST surface a clear, human-readable message rather
  than a raw technical error.
- **FR-012**: The startup configuration-reconciliation process MUST
  preserve the stored enabled/disabled state of existing backends and MUST
  NOT reset a disabled backend to enabled.
- **FR-013**: Every state change MUST be recorded in the audit trail with
  the actor, the backend, and the new state.
- **FR-014**: Each state change MUST ship with its automated tests in the
  same change set, per the project's tests-first principle.

### Key Entities

- **Storage backend**: An S3-compatible blob store known to the platform.
  Already carries identity, connection metadata, and a concurrency
  version; this feature adds a durable **enabled/disabled state**.
- **Backend state change**: An operator action that sets a backend's
  enabled value, carrying the target state and the concurrency version it
  is based on. Authorized, audited, idempotent.
- **Configured default backend**: The single backend designated as the
  platform default; protected from being disabled.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of Paladin-mediated operation types that resolve to a
  disabled backend (upload, download, head, copy, list, presign GET,
  presign PUT, multipart, bucket-create) are refused — verified by an
  automated test exercising each type and asserting no object-store call
  occurs.
- **SC-002**: Re-enabling a backend restores access to pre-existing data
  with zero data loss or modification — verified by reading back, after
  re-enable, an object written before the disable and asserting identical
  content.
- **SC-003**: An attempt to disable the configured default backend is
  refused 100% of the time with a reason that names the constraint.
- **SC-004**: A state change based on a stale concurrency version is
  refused as a conflict 100% of the time, with no silent overwrite.
- **SC-005**: A backend disabled before a platform restart is still
  disabled and still rejecting operations after the restart — verified by
  a restart-and-recheck test.
- **SC-006**: In the admin UI, every disabled backend is shown with a
  "Disabled" badge, is non-selectable as scope, and has its downstream
  operations inactive — verified by an end-to-end UI check with a seeded
  disabled backend.
- **SC-007**: Toggling a backend's state from the UI reflects the new
  state to the operator within 2 seconds.

## Assumptions

- "Storage backend" refers to the existing S3-compatible backends the
  platform already manages; this feature adds state to that existing
  entity rather than introducing a new resource type.
- Strict reject semantics (reads included) are the chosen v1 behaviour. A
  read-only "drain" mode is explicitly out of scope and would be a
  separate, distinctly-named state if ever needed.
- The enabled/disabled state is a simple two-value state. Richer
  lifecycle states (e.g. draining, maintenance, error) are out of scope.
- Already-issued presigned URLs cannot be revoked by this feature and
  expire on their existing short time-to-live; this is an accepted,
  documented limitation, not a defect.
- The existing backend-management permission (platform-admin) is reused;
  no new authorization role or action is introduced.
- The state change reuses the existing optimistic-concurrency mechanism on
  backends; no new concurrency model is introduced.
- Bulk enable/disable of multiple backends in one action is out of scope;
  operators toggle one backend at a time.
