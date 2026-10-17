"use client";

import type React from "react";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

/**
 * One record of a table, opened in its own window — the audit log's entry
 * dialog, made general. A row of a long or wide table shows what fits; the
 * dialog shows all of it, wrapped rather than cut.
 */
export function RecordDialog({
  open,
  onOpenChange,
  title,
  subtitle,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  /** The record's identifier, shown in mono under the title. */
  subtitle?: string;
  children: React.ReactNode;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {subtitle ? (
            <DialogDescription
              className={cn(T.code, "break-all text-muted-foreground")}
            >
              {subtitle}
            </DialogDescription>
          ) : (
            // Radix warns without a description; a screen reader hears the title.
            <DialogDescription className="sr-only">{title}</DialogDescription>
          )}
        </DialogHeader>
        <div className="max-h-[70vh] min-w-0 space-y-3 overflow-y-auto text-sm">
          {children}
        </div>
      </DialogContent>
    </Dialog>
  );
}

/**
 * A field of a record: its label above the value on a narrow screen, beside
 * it from sm up. A mono value is an identifier with no word boundary to wrap
 * at, so it breaks anywhere rather than overflowing the dialog.
 */
export function RecordField({
  label,
  mono,
  children,
}: {
  label: string;
  mono?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="grid gap-0.5 sm:grid-cols-[9rem_minmax(0,1fr)] sm:gap-3">
      <span className={T.label}>{label}</span>
      <span
        className={cn(
          "min-w-0",
          mono ? cn(T.code, "break-all") : "break-words",
        )}
      >
        {children}
      </span>
    </div>
  );
}

/**
 * A block of a record too long for a field — a JSON snapshot, a payload —
 * under its label, scrolling within itself and wrapped rather than cut.
 */
export function RecordBlock({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1">
      <div className={T.label}>{label}</div>
      <pre className="max-h-64 overflow-y-auto rounded-md border border-border bg-muted/40 p-2 font-mono text-sm leading-relaxed whitespace-pre-wrap break-all">
        {children}
      </pre>
    </div>
  );
}

// The controls a row may hold that act on their own: a click or a key on
// one of them is its own, never the row's.
const ROW_CONTROLS =
  "a, button, input, select, textarea, [role=button], [role=menuitem], [role=checkbox]";

function onControl(ev: React.SyntheticEvent): boolean {
  const control = (ev.target as Element).closest(ROW_CONTROLS);
  return (
    control !== null &&
    control !== ev.currentTarget &&
    ev.currentTarget.contains(control)
  );
}

/**
 * What makes a table row open its record: a click, or Enter or Space on the
 * focused row. Spread onto the TableRow. The row keeps its role, so it is
 * still found and read as a row of its table, named by its cells; it says it
 * opens a dialog. A link or button inside the row keeps its own click and
 * keys.
 */
export function openableRow(open: () => void) {
  return {
    tabIndex: 0,
    "aria-haspopup": "dialog" as const,
    className: "cursor-pointer hover:bg-muted/40",
    onClick: (ev: React.MouseEvent) => {
      if (onControl(ev)) return;
      open();
    },
    onKeyDown: (ev: React.KeyboardEvent) => {
      if (ev.target !== ev.currentTarget) return;
      if (ev.key === "Enter" || ev.key === " ") {
        ev.preventDefault();
        open();
      }
    },
  };
}
