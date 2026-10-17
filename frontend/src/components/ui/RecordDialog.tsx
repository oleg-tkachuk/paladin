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
 * What makes a table row open its record: a click, or Enter or Space on the
 * row, which takes focus like a button. Spread onto the TableRow. A key
 * pressed on a control inside the row is that control's, not the row's.
 */
export function openableRow(open: () => void, label: string) {
  return {
    role: "button" as const,
    tabIndex: 0,
    "aria-label": label,
    className: "cursor-pointer hover:bg-muted/40",
    onClick: open,
    onKeyDown: (ev: React.KeyboardEvent) => {
      if (ev.target !== ev.currentTarget) return;
      if (ev.key === "Enter" || ev.key === " ") {
        ev.preventDefault();
        open();
      }
    },
  };
}
