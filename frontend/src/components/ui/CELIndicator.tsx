// Inline status indicator for live CEL validation.
//
// Pair with `useCELValidation` to render a single line of feedback
// directly under the CEL Textarea — green check for valid, red triangle
// + diagnostic for invalid, muted "Validating…" while the RPC is in
// flight, and nothing while idle.
//
// Visual contract:
//   - Sits below the Textarea, sharing T.hint sizing so it slots into
//     the existing helper-text rhythm.
//   - Long error messages wrap inside <pre> rather than overflow the
//     dialog. cel-go diagnostics include source snippets that can run
//     wider than the dialog content area.

import type { JSX } from "react";

import {
  ArrowPathIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import type { CELState } from "@/hooks/useCELValidation";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

export function CELIndicator({
  state,
}: {
  state: CELState;
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
        <span>Valid CEL</span>
      </div>
    );
  }

  // invalid
  const pos =
    state.line !== undefined && state.column !== undefined
      ? `line ${state.line}, col ${state.column}: `
      : "";
  return (
    <div
      className={cn(T.hint, "flex items-start gap-1.5 text-destructive")}
      role="alert"
    >
      <ExclamationTriangleIcon
        className="size-3.5 shrink-0 mt-[1px]"
        aria-hidden
      />
      <pre className="font-mono text-xs whitespace-pre-wrap break-words m-0">
        {pos}
        {state.message}
      </pre>
    </div>
  );
}
