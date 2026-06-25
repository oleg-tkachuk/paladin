// Small presentational pieces for the event subscription editor + list,
// extracted from page.tsx. Layout-only; no client state or RPCs.
import React from "react";
import {
  CheckCircleIcon as CheckCircleSolid,
  ExclamationTriangleIcon as ExclamationSolid,
  XCircleIcon as XCircleSolid,
} from "@heroicons/react/20/solid";

import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { truncate, type TestResult } from "./_form";

export function FormSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-3">
      <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {title}
      </h3>
      <div className="space-y-3">{children}</div>
    </div>
  );
}

export function Field({
  label,
  htmlFor,
  optional,
  hint,
  error,
  children,
}: {
  label: string;
  htmlFor?: string;
  optional?: boolean;
  hint?: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor} className="text-xs">
        {label}
        {optional && (
          <span className="ml-1.5 font-normal text-muted-foreground">
            (optional)
          </span>
        )}
      </Label>
      {children}
      {error ? (
        <p className={cn(T.hint, "text-destructive")}>{error}</p>
      ) : hint ? (
        <p className={T.hint}>{hint}</p>
      ) : null}
    </div>
  );
}

export function ToggleRow<TId extends string>({
  options,
  selected,
  onSelect,
}: {
  options: readonly { id: TId; label: string }[];
  selected: TId | null;
  onSelect: (value: TId) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((opt) => {
        const active = selected === opt.id;
        return (
          <button
            key={opt.id}
            type="button"
            onClick={() => onSelect(opt.id)}
            aria-pressed={active}
            className={cn(
              "rounded-md border px-2.5 py-1 text-xs transition-colors",
              active
                ? "border-primary bg-primary/10 text-primary"
                : "border-input bg-background hover:bg-muted",
            )}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}

// ─── Test result inline display ───────────────────────────────────────
export function TestResultDisplay({ result }: { result: TestResult }) {
  // NATS / non-HTTP sinks: dispatcher returns statusCode=0 on success
  // because the protocol has no broker-level ack analogous to an HTTP
  // 2xx. Treat 0 as "delivered, no status code applicable" and drop
  // the parenthesised number so the chip doesn't read "Delivered (0)".
  if (result.delivered && result.statusCode === 0) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-chart-2">
        <CheckCircleSolid className="size-4" />
        Delivered
      </span>
    );
  }
  if (result.delivered && result.statusCode === 200) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-chart-2">
        <CheckCircleSolid className="size-4" />
        Delivered ({result.statusCode})
      </span>
    );
  }
  if (result.delivered && result.statusCode >= 400) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-chart-3">
        <ExclamationSolid className="size-4" />
        Reached endpoint, HTTP {result.statusCode}
      </span>
    );
  }
  if (result.delivered) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
        <CheckCircleSolid className="size-4" />
        Delivered ({result.statusCode})
      </span>
    );
  }
  return (
    <span
      className="inline-flex items-center gap-1.5 text-xs text-destructive"
      title={result.errorMessage}
    >
      <XCircleSolid className="size-4" />
      Failed: {truncate(result.errorMessage || "unknown error", 64)}
    </span>
  );
}
