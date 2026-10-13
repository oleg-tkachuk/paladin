import React from "react";

import { cn } from "@/lib/utils";

// Presentational form building blocks for the capabilities page (the Issue
// dialog is built from these, with LimitInput from @/components/ui). Pure,
// props-only.

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
