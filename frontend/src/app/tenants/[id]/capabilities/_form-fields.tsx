import React from "react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

// Presentational form building blocks for the capabilities page (the Issue
// dialog is built from these). Extracted from page.tsx — pure, props-only.

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

// Field is one labelled control. `optional` flag drops a low-contrast
// "(optional)" tag next to the label so we don't have to bake the
// hint into the label string itself; `hint` renders below the control
// in muted small text.
export function Field({
  label,
  htmlFor,
  optional,
  hint,
  children,
}: {
  label: string;
  htmlFor?: string;
  optional?: boolean;
  hint?: string;
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
      {hint && <p className={T.hint}>{hint}</p>}
    </div>
  );
}

// ToggleRow renders a horizontal row of pill-buttons for multi-select
// enums. The active state is obvious (filled background) and keyboard
// activation works through standard button semantics (Enter / Space).
export function ToggleRow({
  options,
  selected,
  onToggle,
}: {
  options: readonly string[];
  selected: Set<string>;
  onToggle: (value: string) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((opt) => {
        const active = selected.has(opt);
        return (
          <button
            key={opt}
            type="button"
            onClick={() => onToggle(opt)}
            aria-pressed={active}
            className={cn(
              "rounded-md border px-2.5 py-1 font-mono text-xs transition-colors",
              active
                ? "border-primary bg-primary/10 text-primary"
                : "border-input bg-background hover:bg-muted",
            )}
          >
            {opt}
          </button>
        );
      })}
    </div>
  );
}

// LimitInput pairs a numeric input with an "Unlimited" toggle. When on, the
// input is disabled (the wire value is 0); when off, the input must hold a
// positive number or submit is blocked.
export function LimitInput({
  id,
  value,
  onChange,
  unlimited,
  onUnlimitedChange,
  placeholder,
  type = "number",
  step,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  unlimited: boolean;
  onUnlimitedChange: (next: boolean) => void;
  placeholder?: string;
  type?: string;
  step?: string;
}) {
  return (
    // flex-1 + min-w-0 keeps a type=number input from collapsing to a
    // spinner-only stub in the flex row; the Unlimited button sits at its
    // content width on the right.
    <div className="flex items-center gap-1.5">
      <Input
        id={id}
        type={type}
        step={step}
        min={0}
        value={unlimited ? "" : value}
        onChange={(e) => onChange(e.target.value)}
        disabled={unlimited}
        placeholder={unlimited ? "Unlimited" : placeholder}
        className={cn("min-w-0 flex-1", unlimited && "italic")}
      />
      <button
        type="button"
        onClick={() => onUnlimitedChange(!unlimited)}
        aria-pressed={unlimited}
        className={cn(
          "h-9 shrink-0 rounded-md border px-3 text-xs font-medium transition-colors",
          unlimited
            ? "border-primary bg-primary/10 text-primary"
            : "border-input bg-background hover:bg-muted",
        )}
      >
        Unlimited
      </button>
    </div>
  );
}
