# ADR-0013: Object Lock — retention a mode of Paladin cannot lift

- **Status:** Accepted — implemented 2026-08-21 (`object_locks` table + two
  triggers, `SetObjectRetention` / `SetObjectLegalHold` / `GetObjectLock` on the
  data plane, three Cedar actions, bucket-level default applied at promote
  time, console card).
- **Related:** [ADR-0003](0003-transactional-outbox.md) (mutations and their
  events commit together), [ADR-0012](0012-machine-principals-may-delete-their-own-objects.md)
  (who may delete an object at all).

## Context

WORM retention is a routine ask for anything holding financial records,
medical data or evidence. The shape is standard because S3 set it: a version
carries a retention window in one of two modes, plus an independent legal
hold.

The two modes are not two strengths of the same control. They differ in
exactly one place, and that difference is the feature:

- **GOVERNANCE** has an override. A caller holding `lock.governance.bypass`
  (or `platform.admin`) can shorten the window or delete through it. It
  protects against accident.
- **COMPLIANCE** has no override. Not the tenant admin, not the platform
  admin, not the person who set it, not the operator with a psql session. It
  protects against intent, including one's own.

A control the operator can lift on request is not evidence of anything, which
is the whole reason an auditor asks for the second mode. So the question this
ADR answers is not "how do we store a retention date" — it is "where does the
rule live such that COMPLIANCE means what it says".

Half of this existed already: the table, one trigger, the proto field, the
joins that read it, and an admin RPC storing a bucket-level default. What was
missing was any path that put a version under lock. Nothing called the one
query that could. The feature read as implemented from the schema, the proto
and the console, and enforced nothing.

## Decision

**Retention rules live in the database, not in a handler.**

The application enforces what it can see; a rule in Go binds only the callers
that go through that Go. COMPLIANCE has to survive a caller that does not —
so `object_locks_enforce_no_weakening`, a `BEFORE UPDATE` trigger, is the
boundary. It refuses to shorten an active COMPLIANCE window, refuses to
downgrade its mode, and refuses to shorten an active GOVERNANCE window unless
the session sets `paladin.bypass_governance_retention`.

`SetObjectRetention`'s conditional upsert carries the same rules a second
time. That is not redundancy for its own sake: the clause turns a refusal into
zero rows, which the adapter reports as a clean `FailedPrecondition`, while
the trigger raises. The clause is for the error message; the trigger is for
the guarantee. It also closes the read-then-write race — two callers each
reading a two-year window and each deciding their one-year write is an
extension.

**Deletion is refused in two places, because deletion arrives two ways.**
`object_locks_enforce_retention` fires when the lock row is removed, which is
what a cascading version delete does. The `DELETE FROM objects` statement
carries its own `NOT EXISTS` guard, because that delete never touches
`object_locks` and so never reaches the trigger.

**Legal hold is a separate operation with separate rules.** No expiry,
reversible by whoever may set it, and deliberately outside the governance
bypass — a hold exists to survive exactly the person with the strongest role.
Splitting it from retention in the API is not tidiness: they are delegated to
different people. Answering a preservation request is not the same authority
as pinning storage for seven years.

**Three Cedar actions, none of them `UpdateObject`.** `SetObjectRetention`,
`SetObjectLegalHold`, `ReadObjectLock`. Folding retention into `UpdateObject`
would mean write access to a collection carries the power to make its objects
— and the storage they occupy — permanently undeletable. Legal hold is split
off again because it is reversible and so safe to delegate more widely.

**Object lock requires versioning, in both directions.** A lock attaches to a
version, so a bucket without versioning has nothing to attach one to.
Enabling object lock on such a bucket is refused; so is disabling versioning
under a lock-enabled bucket, which would strand every existing retention with
no way to create the version a future lock needs. S3 has the same
precondition, for the same reason.

**A bucket default is applied at promote time and never overwrites.** An
explicit `SetObjectRetention` that arrived first outranks a default — a
default that clobbers a deliberate choice is worse than no default. Failure to
apply it does not fail the promotion: the object is already AVAILABLE in
storage by then, and refusing to acknowledge that would be a lie.

## Consequences

- **A COMPLIANCE lock is unrecoverable, and that is the point.** A mistaken
  hundred-year window pins the object and its storage cost until it expires.
  The console asks twice and states the consequence in the same breath; the
  API does not, because an API cannot ask.
- **`object_locks_asserts_something` had to go.** Requiring a row to assert
  mode OR legal_hold made clearing a bare hold impossible: the UPDATE violated
  the CHECK and the DELETE hit the retention trigger. A row asserting nothing
  is a released lock, and its timestamps are the record of when the hold was
  placed and lifted.
- **The bypass GUC has one name now.** The writer set
  `paladin.governance_bypass`; the trigger read
  `paladin.bypass_governance_retention`. Two names for one switch means the
  switch was never on — invisible until something actually wrote a lock row
  for the trigger to fire on.
- **`ListObjects` does not report lock state.** `GetObject` and `LookupObject`
  do. Reading a lock per row would add a join to the pagination hot path for a
  field most deployments never set; the proto field says so rather than
  leaving a caller to infer that empty means unlocked.
- **Retention outlives the tenant's wish to delete the tenant.** A tenant with
  COMPLIANCE-locked objects cannot be hard-deleted until they expire. That is
  correct and will surprise someone.
