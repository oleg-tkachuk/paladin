# ADR-0002: Centralized error → Connect-code mapping

- **Status:** Accepted (implemented 2026-06)
- **Context:** Each handler package mapped domain errors to Connect codes
  with its own if/else chain, and the chains drifted — a version mismatch
  was `InvalidArgument` in one RPC and `FailedPrecondition` in another.
  Some sites matched on `err.Error()` substrings (brittle: any wrap or
  upstream message change silently broke classification).

## Decision

A single chokepoint, `apiutil.MapError(err) error`, with canonical
domain sentinels (`ErrNotFound`, `ErrConflict`, `ErrAlreadyExists`,
`ErrInvalidArgument`, `ErrPermissionDenied`, `ErrFailedPrecondition`,
`ErrUnauthenticated`) → Connect codes via one table. Resolution order:
already-a-`*connect.Error` passes through (a handler that chose a precise
code wins) → `errors.Is` against canonical sentinels → a registry → else
`CodeInternal`. All matching is `errors.Is`, never string compare.

To avoid an import cycle (apiutil is imported *by* handlers), packages
keep their own sentinels and register them at init via
`apiutil.RegisterError(sentinel, code)` instead of forcing apiutil to
import every handler package. The object package registers
`ErrVersionMismatch → Aborted` and `ErrBackendDisabled →
FailedPrecondition`.

## Consequences

- Codes are consistent across RPCs; adding a mapping is one table/registry
  line.
- The `internal/api/v1` plane is fully adopted: every package that owns
  domain sentinels (`object`, `object_key`, `object_tag`, `bucket`,
  `tenant`) registers them in an `init()` and collapses its
  `CodeInternal`-default classification ladders to `apiutil.MapError(err)`.
  A per-package `TestErrorRegistration` pins each sentinel→code so the
  collapsed ladders cannot drift. Ladders with a deliberate non-`Internal`
  default (`object_key.Rebind` → `FailedPrecondition`, `object.MapResolveErr`
  → `NotFound`) are left intact — `MapError` defaults to `CodeInternal` and
  cannot replicate those.
- Remaining planes (`internal/api/admin/v1`, `internal/api/iam/v1`) still
  use per-handler if/else over their own sentinels — see BACKLOG.
- `.Error()` substring matching is forbidden going forward.
