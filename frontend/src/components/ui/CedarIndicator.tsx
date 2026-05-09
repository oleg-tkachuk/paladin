// Inline status indicator for live Cedar validation.
//
// Pair with `useCedarValidation` to render feedback directly under the
// Cedar Textarea. Unlike CELIndicator, Cedar's parser can return many
// diagnostics at once (one per offending rule); we render them as a
// stacked list so the operator sees the full set without iterating
// "fix one, see the next."
//
// Visual contract:
//   - Errors render in `text-destructive`, warnings in `text-chart-3`.
//   - Each row shows severity icon + message + line/col when known.
//   - Sits below the Textarea with the same T.hint sizing rhythm used
//     by CELIndicator so the two readers feel consistent.

import type { JSX } from "react";

import {
  ArrowPathIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import type { CedarState } from "@/hooks/useCedarValidation";
import type { PolicyDiagnostic } from "@/gen/paladin/admin/v1/policy_service_pb";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

export function CedarIndicator({
  state,
}: {
  state: CedarState;
}): JSX.Element | null {
  if (state.status === "idle") return null;

  if (state.status === "validating") {
    return (
      <div className={cn(T.hint, "flex items-center gap-1.5")}>
        <ArrowPathIcon className="size-3.5 animate-spin" aria-hidden />
        <span>Validating…</span>
      </div>
    );
  }

  if (state.status === "valid") {
    return (
      <div className={cn(T.hint, "flex items-center gap-1.5 text-chart-2")}>
        <CheckCircleIcon className="size-3.5" aria-hidden />
        <span>Valid Cedar</span>
      </div>
    );
  }

  // invalid — stack each diagnostic on its own row.
  return (
    <div className="space-y-1" role="alert">
      {state.diagnostics.map((d, i) => (
        <DiagnosticLine key={i} diag={d} />
      ))}
    </div>
  );
}

function DiagnosticLine({ diag }: { diag: PolicyDiagnostic }): JSX.Element {
  const isError = diag.severity.toLowerCase() === "error";
  const tone = isError ? "text-destructive" : "text-chart-3";
  const pos =
    diag.line > 0 || diag.column > 0
      ? `line ${diag.line}, col ${diag.column}: `
      : "";
  return (
    <div className={cn(T.hint, "flex items-start gap-1.5", tone)}>
      <ExclamationTriangleIcon
        className="size-3.5 shrink-0 mt-[1px]"
        aria-hidden
      />
      <pre className="font-mono text-xs whitespace-pre-wrap break-words m-0">
        {pos}
        {diag.message}
      </pre>
    </div>
  );
}
