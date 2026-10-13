import React from "react";

import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

// LimitInput pairs a numeric input with an "Unlimited" toggle. When on, the
// input is disabled and the form sends "no limit"; when off, the input holds
// the limit, where 0 allows nothing, and the form blocks submit until it does.
export function LimitInput({
  id,
  value,
  onChange,
  unlimited,
  onUnlimitedChange,
  placeholder,
  type = "number",
  step,
  "aria-describedby": describedBy,
  "aria-invalid": invalid,
}: {
  id: string;
  "aria-describedby"?: string;
  "aria-invalid"?: true;
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
        aria-describedby={describedBy}
        aria-invalid={invalid}
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
