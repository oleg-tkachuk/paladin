// Live CEL filter validation hook.
//
// Calls the admin-plane CELService.Validate RPC with a debounced version
// of the operator's expression and exposes the result as a small state
// machine. Used by the lifecycle rule editor (`match` against Object)
// and the event subscription editor (`filter` against EventEnvelope) to
// surface compile errors at edit-time instead of at apply-time.
//
// Behaviour:
//   - Empty expression short-circuits to "valid" — matches the backend
//     match-all sentinel (cel.Validate / cel.CompileFirstError).
//   - Debounced (default 350ms). Each new expression resets the timer.
//   - In-flight RPCs are cancelled by AbortController when the input
//     changes, so the UI never flashes stale results.
//   - ConnectError is caught and surfaced as `invalid` with the raw
//     message; transport failures look the same as compile failures
//     to the operator (both block submit).

import { useEffect, useRef, useState } from "react";
import { ConnectError } from "@connectrpc/connect";

import { celClient } from "@/lib/connect/client";

export type CELSchema =
  | "Object"
  | "ObjectKey"
  | "AuditLogEntry"
  | "EventEnvelope";

export type CELState =
  | { status: "idle" }
  | { status: "validating" }
  | { status: "valid" }
  | {
      status: "invalid";
      message: string;
      line?: number;
      column?: number;
    };

interface Options {
  debounceMs?: number;
}

const DEFAULT_DEBOUNCE_MS = 350;

export function useCELValidation(
  expression: string,
  schema: CELSchema,
  opts?: Options,
): CELState {
  const debounceMs = opts?.debounceMs ?? DEFAULT_DEBOUNCE_MS;
  const isEmpty = expression.trim() === "";
  // asyncState only ever reflects the latest RPC outcome. The "empty
  // → valid" short-circuit is derived at render-time below so we never
  // call setState synchronously inside the effect (react-hooks/
  // set-state-in-effect). The "validating" transition is set inside
  // the setTimeout callback — async by definition, so it's allowed.
  const [asyncState, setAsyncState] = useState<CELState>({ status: "idle" });
  // Track the latest in-flight request so we can ignore stale responses
  // even if the AbortController doesn't fire fast enough on the network.
  const reqIdRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    // Empty = valid (match-all). Cancel any in-flight RPC and bail —
    // the render-time return below picks up the empty case directly,
    // so no setState needed here.
    if (isEmpty) {
      abortRef.current?.abort();
      reqIdRef.current += 1;
      return;
    }

    const reqId = ++reqIdRef.current;
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;

    const timer = setTimeout(() => {
      // Inside the timer — async w.r.t. the effect body, so setState
      // here doesn't trip the cascading-render lint rule.
      setAsyncState({ status: "validating" });
      celClient
        .validate({ schema, expression }, { signal: controller.signal })
        .then((resp) => {
          if (reqId !== reqIdRef.current) return; // stale
          if (resp.valid) {
            setAsyncState({ status: "valid" });
          } else {
            setAsyncState({
              status: "invalid",
              message: resp.message || "Invalid CEL expression.",
              line: resp.line > 0 ? resp.line : undefined,
              column: resp.column > 0 ? resp.column : undefined,
            });
          }
        })
        .catch((err: unknown) => {
          if (reqId !== reqIdRef.current) return; // stale
          // AbortError surfaces here on cancel; treat as a no-op so
          // the next iteration's "validating" state isn't overwritten
          // by "invalid: aborted".
          if (
            err instanceof DOMException &&
            (err.name === "AbortError" || err.code === DOMException.ABORT_ERR)
          ) {
            return;
          }
          if (err instanceof ConnectError) {
            setAsyncState({
              status: "invalid",
              message: err.rawMessage || err.message,
            });
            return;
          }
          setAsyncState({
            status: "invalid",
            message: err instanceof Error ? err.message : String(err),
          });
        });
    }, debounceMs);

    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [expression, schema, debounceMs, isEmpty]);

  // Render-time derivation: empty expression always reads as valid,
  // regardless of stale asyncState carried over from a previous typing
  // session. Avoids the "user clears the input but the previous error
  // still shows" UX bug and keeps the effect free of synchronous
  // setState calls.
  return isEmpty ? { status: "valid" } : asyncState;
}
