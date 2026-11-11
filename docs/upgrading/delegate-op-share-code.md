## Unreleased — a delegation without `share` carries limes's code

- `CapabilityService.Delegate` from a parent whose caveats lack `share` is
  still `PERMISSION_DENIED`. It now carries the limes code `op_not_allowed`
  and the capability reason in its `ErrorInfo`, as the other refusals limes
  makes do. The message is that problem's title, no longer
  "capability: caller lacks OpShare".
