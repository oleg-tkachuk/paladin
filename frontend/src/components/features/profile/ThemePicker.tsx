"use client";

import {
  THEME_DARK,
  THEME_LIGHT,
  THEME_OPTIONS,
  THEME_SYSTEM,
  type Theme,
} from "@/lib/theme";
import { cn } from "@/lib/utils";

/**
 * The themes as swatches. Each swatch is drawn in its own palette — the
 * theme's class on the swatch scopes globals.css' colour variables to it —
 * so it shows the real colours, never a copy of them. "Match system" is
 * half light, half dark.
 */
export function ThemePicker({
  value,
  onChange,
  labelledBy,
}: {
  value: string;
  onChange: (theme: Theme) => void;
  labelledBy: string;
}) {
  return (
    <div
      role="radiogroup"
      aria-labelledby={labelledBy}
      className="grid grid-cols-2 gap-3 sm:grid-cols-4"
    >
      {THEME_OPTIONS.map((o) => {
        const selected = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={selected}
            onClick={() => onChange(o.value)}
            className={cn(
              "group rounded-lg border p-1.5 text-left transition-colors",
              selected
                ? "border-primary ring-2 ring-primary/40"
                : "border-border hover:border-muted-foreground/50",
            )}
          >
            {o.value === THEME_SYSTEM ? (
              <span className="grid grid-cols-2 overflow-hidden rounded-md">
                <Swatch palette={THEME_LIGHT} half />
                <Swatch palette={THEME_DARK} half />
              </span>
            ) : (
              <Swatch palette={o.value} />
            )}
            <span className="mt-1.5 block px-0.5 text-xs font-medium">
              {o.label}
            </span>
          </button>
        );
      })}
    </div>
  );
}

function Swatch({ palette, half }: { palette: string; half?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        palette,
        "flex h-14 flex-col gap-1 bg-background p-2",
        half ? "" : "rounded-md",
      )}
    >
      <span className="h-1.5 w-8 rounded-full bg-primary" />
      <span className="h-1 w-12 rounded-full bg-foreground/70" />
      <span className="h-1 w-10 rounded-full bg-muted-foreground/50" />
    </span>
  );
}
