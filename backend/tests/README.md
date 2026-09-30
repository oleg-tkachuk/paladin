# Backend tests outside the packages

Unit tests live beside the code they test, in `internal/` and `cmd/`, and run
with `task backend:test`. This directory holds everything that needs more than
one package, a database, a storage endpoint or a running stack.

| Directory | What it asserts | Needs | Run with |
| --- | --- | --- | --- |
| `contract/` | Static gates over the API surface: a proto field the server declares is actually read, an OCC-guarded update checks its version. Reads source and descriptors, talks to nothing. | nothing | `task backend:test` |
| `integration/` | The assembled application against real Postgres: wiring, mux mounts, the whole RPC surface, RLS coverage, the transactional outbox, the SQL prepare and scan gates. Shared harness in `integration/pgharness/`. | Docker (testcontainers) | `task backend:test:integration` |
| `integration/components/` | One component at a time against real Postgres, S3 or a broker: store adapters, workers, auth stores, sinks, benchmarks. | Docker (testcontainers) | `task backend:test:integration`, `task backend:test:bench` |
| `conformance/` | What an S3-compatible backend must do for Paladin to work; see its [README](conformance/README.md). | an S3 endpoint | `go test -tags=conformance ./tests/conformance/` |
| `e2e/` | The admin Connect API walked end to end against a deployed stack. | a running stack | `task backend:test:stack` |
| `api/security/` | SAST (gosec), DAST (nuclei, ZAP) and SCA (trivy) wrappers. | the tools, and a running stack for DAST | `task backend:test:security` |
| `api/security-probe.sh` | Adversarial tenant-isolation probe: crafted JWTs that spoof tenant, slug or audience must be rejected. | a running cluster | `backend/tests/api/security-probe.sh` |

Everything that needs Docker or a stack is collected by
`task -t Taskfile.dev.yaml verify-deep` at the repository root.

The `integration`, `conformance` and `e2e` suites sit behind build tags of the
same names, so `go test ./...` and `verify-all` never start a container.
