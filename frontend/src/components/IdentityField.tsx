"use client";

// IdentityField — a labelled metadata row with optional lock icon
// (immutable indicator) and copy-to-clipboard button. Used wherever
// the UI surfaces a tenant_id / slug / backend_id / bucket_name in a
// read-only context: overview cards, edit dialogs, sidebar identity
// summaries. Centralising the affordance set means lock semantics
// and copy UX stay consistent across the app.

import React, { useState } from "react";
import {
  CheckIcon,
  ClipboardIcon,
  LockClosedIcon,
} from "@heroicons/react/24/outline";

import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

export interface IdentityFieldProps {
  /** Short label rendered in muted uppercase. */
  label: string;
  /** Value to display + copy. */
  value: string;
  /** When true, render a lock icon next to the value. */
  immutable?: boolean;
  /** When true, render with `truncate` + `title` attr — needed for
   *  long UUIDs on narrow viewports. */
  truncate?: boolean;
  /** When true, render value in monospaced T.code font. Defaults to
   *  true for IDs; falsy strings + short labels can opt out. */
  mono?: boolean;
  /** Width of the label column (Tailwind class). Defaults `w-16`. */
  labelWidth?: string;
  /** Optional ID for the value cell (for ARIA labelling). */
  id?: string;
  /** Optional className on the outer container. */
  className?: string;
}

export function IdentityField({
  label,
  value,
  immutable = false,
  truncate = false,
  mono = true,
  labelWidth = "w-16",
  id,
  className,
}: IdentityFieldProps) {
  const [copied, setCopied] = useState(false);
  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      // Clipboard API unavailable (e.g. http context) — silent.
    }
  };

  return (
    <div className={cn("flex items-center gap-2 text-xs", className)}>
      <span
        className={cn(
          labelWidth,
          "uppercase tracking-wider text-muted-foreground",
        )}
      >
        {label}
      </span>
      <span
        id={id}
        className={cn(
          mono && T.code,
          truncate && "truncate text-muted-foreground text-[11px] min-w-0",
        )}
        title={truncate ? value : undefined}
      >
        {value || <span className="italic text-muted-foreground">—</span>}
      </span>
      {immutable && (
        <LockClosedIcon
          className="size-3 shrink-0 text-muted-foreground/70"
          aria-label="immutable"
        />
      )}
      <button
        type="button"
        onClick={handleCopy}
        disabled={!value}
        className="ml-auto inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-30 disabled:hover:bg-transparent"
        aria-label={`Copy ${label}`}
      >
        {copied ? (
          <>
            <CheckIcon className="size-3" /> copied
          </>
        ) : (
          <>
            <ClipboardIcon className="size-3" /> copy
          </>
        )}
      </button>
    </div>
  );
}
