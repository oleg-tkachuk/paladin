# Backend tests outside the packages

Unit tests live beside the code they test, in `internal/` and `cmd/`, and run
with `task backend:test`. This directory holds everything that needs more than
one package, a database, a storage endpoint or a running stack.

| Directory | What it asserts | Needs | Run with |
| --- | --- | --- | --- |
| `contract/` | Static gates over the API surface: a proto field the server declares is actually read, an OCC-guarded update checks its version. Reads source and descriptors, talks to nothing. | nothing | `task backend:test` |
| `integration/` | The assembled application against real Postgres: wiring, mux mounts, the whole RPC surface, RLS coverage, the transactional outbox, the SQL prepare and scan gates. Shared harness in `integration/pgharness/`. | Docker (testcontainers) | `task backend:test:integration` |
| `integration/components/` | One component at a time against real Postgres, S3 or a broker: store adapters, workers, auth stores, sinks, benchmarks. | Docker (testcontainers) | `task backend:test:integration`, `task backend:test:bench` |
| `conformance/` | What an S3-compatible backend must do for Paladin to work; see its [README](conformance/README.md). | an S3 endpoint | `go test -tags=conformance ./tests/conformance/`; `task backend:test:stack` runs it against the stack's MinIO |
| `e2e/` | The admin Connect API walked end to end against a deployed stack. | a running stack | `task backend:test:stack` |
| `api/security/` | SAST (gosec), DAST (nuclei, ZAP) and SCA (trivy) wrappers. | the tools, and a running stack for DAST | `task backend:test:security` |
| `api/security-probe.sh` | Adversarial tenant-isolation probe: crafted JWTs that spoof tenant, slug or audience must be rejected. | a running cluster | `backend/tests/api/security-probe.sh` |

A few `integration`-tagged tests live inside the package they test instead:
`internal/worker/` (the SQS sink's batch path against elasticmq) and
`internal/worker/lease/` (the lease claim SQL against Postgres).
`task backend:test:integration` finds every package holding an
`integration`-tagged test file rather than listing them, so these run with the
rest.

Everything that needs Docker or a stack is collected by
`task -t Taskfile.dev.yaml verify-deep` at the repository root.

The `integration`, `conformance` and `e2e` suites sit behind build tags of the
same names, so `go test ./...` and `verify-all` never start a container.

## One Postgres per test binary, one database per test

The two Postgres-backed packages under `tests/` share one container per test
binary and give every test a database of its own, cloned from a template the
migrations ran into once: `pgharness.Setup` in `integration/`, `startPostgres`
in `integration/components/`. A clone takes a fraction of a second where a
container took seconds, and the clone is dropped when the test ends.

Tables, sequences, LISTEN/NOTIFY channels and advisory locks are per
database, so a test sees nothing another wrote, and most tests run with
`t.Parallel`. What the server shares, a test must not disturb:

- **Roles are server-wide.** Configure them in the harness, once. A test that
  needs a role of its own names it after its database (`rlsPool` does).
- **`pg_stat_activity` lists every database.** Filter on
  `datname = current_database()` before terminating or counting backends.
- **A test that alters a shared role, sets goose's package globals or calls
  `t.Setenv` stays serial**, with a first-line comment saying which.

A package using `pgharness` calls `os.Exit(pgharness.Main(m))` from its
`TestMain`, so the container stops with the binary.
