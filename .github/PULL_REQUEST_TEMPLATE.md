## What and why

<!-- The diff shows what changed. Use this space for why it needed to. -->

## How it was verified

<!--
Which of these actually ran, and what they said. "CI will tell us" is not a
verification step.
-->

- [ ] `task verify-all` — the fast gate: unit, lint, build, tagged-suite compile
- [ ] `task verify-deep` — the slow one: Postgres-backed integration suites,
      then the RPC surface, the Go admin e2e suite, S3 conformance and
      `dev-bootstrap.sh` against a freshly built stack. ~15 min and a Docker daemon. Skipping it
      is how a suite that compiles but fails reaches `main`.
- [ ] `pnpm run test:e2e` (if the console or a plane's wire format changed)
- [ ] Manual check against a running stack — say what you did

## Checklist

- [ ] Commit subjects follow Conventional Commits, one scope each
- [ ] New behaviour has a test
- [ ] Anything deliberately left undone is recorded in `BACKLOG.md`, with
      Status / Reason / Definition of Done / Blockers
- [ ] Any completed `BACKLOG.md` entry is **deleted** in this PR, not
      marked done
- [ ] A boundary change (dependency direction, trust relationship, storage
      or transport choice) has an ADR in `docs/adr/`
- [ ] No credentials, tokens or internal hostnames added to tracked files

## Related

<!-- Issues, ADRs, BACKLOG entries, spec directories. -->
