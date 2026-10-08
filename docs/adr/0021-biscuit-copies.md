# ADR-0021: Biscuit copies — narrowed offline, revoked and counted by block

- **Status:** Accepted 2026-10-04.

- **Context.** A capability can be handed out as a Biscuit, which its holder
  narrows with no key and no call to the server: an orchestrator gives each
  worker a smaller copy, and a worker its sub-agents a smaller one still.
  Every copy carried only the capability's id, so the copies of one capability
  were indistinguishable to the server: revoking one meant revoking the
  capability and every agent on it, and all of them spent one allowance, so a
  single runaway copy could exhaust it for the rest.

- **Decision.**
  - **A copy is named by its blocks.** Every block of a Biscuit has a
    revocation id, its signature, and a copy carries the ids of every block
    above it. An id therefore names a copy and everything attenuated from it,
    and nothing above or beside it. Revocation and limits both key on it; the
    server keeps no list of copies, which exist only in their holders' hands.
  - **Revoking a copy lists one id.** `CapabilityService.RevokeBiscuit` takes
    the copy itself, checks it is genuine, and lists its last block's id; the
    verifier refuses a token carrying any listed id, after the capability's
    own revocation. Revoking the capability still stops every copy and the JWT.
  - **A copy may carry limits of its own.** Two facts join the attenuation
    vocabulary, `paladin_max_requests` and `paladin_max_budget_nanos` (the copy's
    budget in billionths of the capability's unit), each
    within every limit already in force. They are counted under the block's
    id, beside the capability's counters, which still bound all copies
    together; a charge or reservation records the ids it debited, so refunds,
    settles and releases return to them. `GetBiscuitUsage` reads them back.
  - **The verifier refuses what it cannot enforce.** Copy limits are admitted
    only by a verifier told, with `MeterCopies`, that its `Meter` counts them;
    otherwise the token is refused. Datalog rules and checks stay outside the
    vocabulary for the same reason.
  - **Delegation from a copy narrows from the copy.** A server-side child is
    narrowed from the capability as presented, not from its stored record,
    and a copy with limits of its own may not delegate at all: the child would
    count against the capability, never against the copy.

- **Consequences.**
  - The module gains two optional contracts, `BiscuitRevocationStore` and
    `CopyUsageReader`, and its `Meter` requests carry `Copies`. A consumer
    without Biscuits implements neither; one with Biscuits but no copy
    counting gets refusals, not unkept limits.
  - Postgres keeps two small tables keyed by revocation id
    (`capability_biscuit_revocations`, `capability_copy_usage`), isolated
    through the capability and purged with it; a row exists only for a copy
    that was revoked or used under a limit of its own.
  - An operator sees a copy only when someone pastes it: the console's
    "Revoke a copy" and "Copy usage" take the token, because nothing else
    names it.
  - Per-copy counters follow the attenuation chain, not the delegation tree:
    a server-side child is metered against its ancestors' records as before.
