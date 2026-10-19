"use client";

import React from "react";
import { ClipboardIcon, CheckIcon } from "@heroicons/react/24/outline";
import { useConfirmation } from "@/hooks/useConfirmation";
import { cn } from "@/lib/utils";
import { PRESSED } from "@/lib/ui/states";

/** What the live region says once the value is on the clipboard. */
export const COPIED_NOTICE = "Copied";

interface IdentifierCopyProps {
  value: string;
  label: string;
  className?: string;
  iconOnly?: boolean;
}

export function IdentifierCopy({
  value,
  label,
  className,
  iconOnly,
}: IdentifierCopyProps) {
  // Confirmed where it happened — the tick on the button, said to assistive
  // technology through the live region — not by a toast across the screen.
  const { shown: copied, confirm } = useConfirmation();

  const handleCopy = async (e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(value);
      confirm();
    } catch (err) {
      console.error("Failed to copy", err);
    }
  };

  if (iconOnly) {
    return (
      <button
        type="button"
        onClick={handleCopy}
        className={cn(
          "p-2 rounded-xl transition-all group/copy",
          PRESSED,
          copied
            ? "bg-success/10 text-success"
            : "bg-muted/50 text-muted-foreground hover:text-foreground hover:bg-muted",
          className,
        )}
        title={`Copy ${label}`}
        aria-label={`Copy ${label}`}
      >
        {copied ? (
          <CheckIcon className="w-4 h-4" />
        ) : (
          <ClipboardIcon className="w-4 h-4 group-hover/copy:scale-110 transition-transform" />
        )}
        <span className="sr-only" aria-live="polite">
          {copied ? COPIED_NOTICE : ""}
        </span>
      </button>
    );
  }

  return (
    // A button, not a clickable div: a div cannot be reached or pressed from
    // the keyboard, so the identifier could only be copied with a mouse.
    <button
      type="button"
      onClick={handleCopy}
      title={`Copy ${label}`}
      className={cn(
        "group/copy relative flex w-full items-center justify-between p-4 text-left rounded-2xl bg-muted/40 border border-border hover:border-primary/30 transition-all cursor-pointer overflow-hidden",
        className,
      )}
    >
      <span className="absolute inset-0 bg-primary/[0.02] opacity-0 group-hover/copy:opacity-100 transition-opacity" />
      <span className="block space-y-1 min-w-0 pr-10">
        <span className="block text-xs font-semibold text-primary/60 uppercase tracking-wider">
          {label}
        </span>
        <span className="block text-xs text-foreground font-mono truncate">
          {value}
        </span>
      </span>
      <span
        className={cn(
          "p-2 rounded-lg transition-all",
          copied
            ? "text-success"
            : "text-muted-foreground group-hover/copy:text-foreground",
        )}
      >
        {copied ? (
          <CheckIcon className="w-4 h-4" />
        ) : (
          <ClipboardIcon className="w-4 h-4" />
        )}
      </span>
      <span className="sr-only" aria-live="polite">
        {copied ? COPIED_NOTICE : ""}
      </span>
    </button>
  );
}
