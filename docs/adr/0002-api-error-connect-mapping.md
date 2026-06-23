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
- Adoption is incremental: the chokepoint exists and `errors.Is`-based
  matching means existing local checks keep working; handlers route their
  generic `CodeInternal` fallthroughs through `MapError` as they're
  touched, rather than a big-bang sweep.
- `.Error()` substring matching is forbidden going forward.
