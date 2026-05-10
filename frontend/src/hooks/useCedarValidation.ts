// Live Cedar policy validation hook.
//
// Mirrors useCELValidation: a debounced, AbortController-backed call to
// PolicyService.Validate that surfaces compile diagnostics inline as
// the operator types. The Cedar variant differs from CEL in one key
// way — Cedar's parser returns a *list* of diagnostics (one per
// offending rule), each carrying its own severity / line / column.
// We surface the full list so the editor can show errors and warnings
// stacked rather than collapsing to a single "first error" line.
//
// Behaviour:
//   - Empty policy → "valid" (the backend treats empty as match-all
//     and accepts a save). Avoids flashing red on a freshly cleared
//     textarea.
//   - Debounced (default 350ms) with AbortController + reqId stale-
//     response guard, identical to the CEL hook.
//   - Transport / Connect failures collapse to a single synthetic
//     error diagnostic so the indicator can render uniformly.

import { useEffect, useRef, useState } from "react";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { policyClient } from "@/lib/connect/client";
import {
  PolicyDiagnosticSchema,
  type PolicyDiagnostic,
} from "@/gen/paladin/admin/v1/policy_service_pb";

export type CedarState =
  | { status: "idle" }
  | { status: "validating" }
  | { status: "valid" }
  | { status: "invalid"; diagnostics: PolicyDiagnostic[] };

interface Options {
  debounceMs?: number;
}

const DEFAULT_DEBOUNCE_MS = 350;

export function useCedarValidation(
  cedarPolicy: string,
  opts?: Options,
): CedarState {
  const debounceMs = opts?.debounceMs ?? DEFAULT_DEBOUNCE_MS;
  const isEmpty = cedarPolicy.trim() === "";
  const [asyncState, setAsyncState] = useState<CedarState>({ status: "idle" });
  const reqIdRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
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
      setAsyncState({ status: "validating" });
      policyClient
        .validate({ cedarPolicy }, { signal: controller.signal })
        .then((resp) => {
          if (reqId !== reqIdRef.current) return;
          if (resp.ok && resp.diagnostics.length === 0) {
            setAsyncState({ status: "valid" });
          } else if (resp.diagnostics.length === 0) {
            // ok=false but no diagnostics — surface a synthetic one
            // so the indicator never goes silent on failure.
            setAsyncState({
              status: "invalid",
              diagnostics: [
                create(PolicyDiagnosticSchema, {
                  severity: "error",
                  message: "Policy rejected without diagnostics.",
                }),
              ],
            });
          } else {
            setAsyncState({
              status: "invalid",
              diagnostics: resp.diagnostics,
            });
          }
        })
        .catch((err: unknown) => {
          if (reqId !== reqIdRef.current) return;
          if (
            err instanceof DOMException &&
            (err.name === "AbortError" || err.code === DOMException.ABORT_ERR)
          ) {
            return;
          }
          const message =
            err instanceof ConnectError
              ? err.rawMessage || err.message
              : err instanceof Error
                ? err.message
                : String(err);
          setAsyncState({
            status: "invalid",
            diagnostics: [
              create(PolicyDiagnosticSchema, {
                severity: "error",
                message,
              }),
            ],
          });
        });
    }, debounceMs);

    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [cedarPolicy, debounceMs, isEmpty]);

  return isEmpty ? { status: "valid" } : asyncState;
}

// True iff at least one diagnostic carries error severity. Warnings
// alone don't block save — the backend accepts them.
export function hasCedarErrors(state: CedarState): boolean {
  if (state.status !== "invalid") return false;
  return state.diagnostics.some((d) => d.severity.toLowerCase() === "error");
}
