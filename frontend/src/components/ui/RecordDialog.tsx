"use client";

import type React from "react";
import { CheckIcon, ClipboardIcon } from "@heroicons/react/24/outline";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useConfirmation } from "@/hooks/useConfirmation";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { FOCUS_RING } from "@/lib/ui/states";

// A value longer than this reads better under its label than beside it: it
// would wrap in a narrow column into a block of fragments.
const RECORD_INLINE_MAX_CHARS = 48;

// What the dialog's content is told about itself, read by its width: a field
// too long to sit beside its label, and a block of text, each widen it.
// RECORD_DIALOG_WIDTH spells both names out, since Tailwind finds a class
// only as literal text; a test holds the two together.
const WIDE_FIELD = "data-record-wide";
const BLOCK = "data-record-block";
const RECORD_DIALOG_WIDTH =
  "sm:max-w-lg sm:has-[[data-record-wide]]:max-w-2xl sm:has-[[data-record-block]]:max-w-3xl";

/**
 * One record of a table, opened in its own window. A row of a long or wide
 * table shows what fits; the dialog shows all of it.
 *
 * It sizes itself to what it holds: narrow for a handful of short fields, wider
 * when a field's value is long, widest for a block such as a JSON snapshot. The
 * fields share one column of labels, as wide as the longest label rather than
 * a fixed width, so short labels do not leave a gutter beside every value.
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
  /** The record's identifier, shown in mono under the title, with a copy. */
  subtitle?: string;
  children: React.ReactNode;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className={cn("gap-0 p-0", RECORD_DIALOG_WIDTH)}>
        <DialogHeader className="gap-1 border-b border-border px-5 pt-5 pb-3 pr-12">
          <DialogTitle>{title}</DialogTitle>
          {subtitle ? (
            <div className="flex min-w-0 items-start gap-1">
              <DialogDescription
                className={cn(
                  T.codeSmall,
                  "min-w-0 break-all text-muted-foreground",
                )}
              >
                {subtitle}
              </DialogDescription>
              <CopyButton
                value={subtitle}
                label={`Copy ${title.toLowerCase()} id`}
              />
            </div>
          ) : (
            // Radix warns without a description; a screen reader hears the title.
            <DialogDescription className="sr-only">{title}</DialogDescription>
          )}
        </DialogHeader>
        <RecordFields className="max-h-[70vh] overflow-y-auto px-5 py-4">
          {children}
        </RecordFields>
      </DialogContent>
    </Dialog>
  );
}

/**
 * The fields of a record, sharing one column of labels as wide as the longest
 * label. The body of a RecordDialog, and of a record shown in a DetailPanel.
 */
export function RecordFields({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        "grid min-w-0 grid-cols-1 content-start gap-y-3 text-sm",
        "sm:grid-cols-[fit-content(11rem)_minmax(0,1fr)] sm:gap-x-6",
        // Anything that is not a field — a hint, an action, a block —
        // spans both columns.
        "[&>*]:col-span-full",
        className,
      )}
    >
      {children}
    </div>
  );
}

/**
 * A field of a record. Beside its label from sm up, in the dialog's shared
 * label column; above it on a narrow screen, and above it at any width when
 * the value is too long to sit beside it (or `wide` says so).
 *
 * A mono value is an identifier: it breaks anywhere rather than overflowing,
 * and when it is plain text it can be copied.
 */
export function RecordField({
  label,
  mono,
  wide,
  children,
}: {
  label: string;
  mono?: boolean;
  /** Put the value under its label, whatever its length. */
  wide?: boolean;
  children: React.ReactNode;
}) {
  const text = typeof children === "string" ? children : null;
  const stacked =
    wide || (text !== null && text.length > RECORD_INLINE_MAX_CHARS);
  const value = (
    <span
      className={cn("min-w-0", mono ? cn(T.code, "break-all") : "break-words")}
    >
      {children}
    </span>
  );
  return (
    <div
      {...(stacked ? { [WIDE_FIELD]: "" } : {})}
      className={cn(
        // Row gap only: a gap on both axes would replace the gap between the
        // label column and the values that the subgrid takes from the dialog.
        "grid min-w-0 gap-y-0.5",
        stacked ? "sm:gap-y-1" : "sm:grid-cols-subgrid sm:items-baseline",
      )}
    >
      <span className={T.helper}>{label}</span>
      {mono && text ? (
        <span className="group flex min-w-0 items-start gap-1">
          {value}
          <CopyButton value={text} label={`Copy ${label.toLowerCase()}`} />
        </span>
      ) : (
        value
      )}
    </div>
  );
}

/**
 * A block of a record too long for a field — a JSON snapshot, a payload —
 * under its label, scrolling within itself and wrapped rather than cut. It
 * widens the dialog, and can be copied whole.
 */
export function RecordBlock({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  const text = typeof children === "string" ? children : null;
  return (
    <div {...{ [BLOCK]: "" }} className="space-y-1">
      <div className="flex items-center justify-between gap-2">
        <span className={T.helper}>{label}</span>
        {text ? (
          <CopyButton value={text} label={`Copy ${label.toLowerCase()}`} />
        ) : null}
      </div>
      <pre className="max-h-72 overflow-y-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all">
        {children}
      </pre>
    </div>
  );
}

/**
 * Copies value, and says so on itself for a moment. No toast: a record's
 * dialog or panel is opened from many pages, not all of them inside a
 * notifier.
 */
export function CopyButton({ value, label }: { value: string; label: string }) {
  const { shown: copied, confirm } = useConfirmation();
  const Icon = copied ? CheckIcon : ClipboardIcon;
  return (
    <button
      type="button"
      aria-label={copied ? "Copied" : label}
      title={copied ? "Copied" : label}
      onClick={() => {
        void navigator.clipboard?.writeText(value).then(confirm);
      }}
      className={cn(
        "shrink-0 rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground",
        FOCUS_RING,
      )}
    >
      <Icon className="size-3.5" />
    </button>
  );
}

// The controls a row may hold that act on their own: a click or a key on
// one of them is its own, never the row's.
const ROW_CONTROLS =
  "a, button, input, select, textarea, [role=button], [role=menuitem], [role=checkbox]";

function onControl(ev: React.SyntheticEvent): boolean {
  // React bubbles an event out of a portal along the component tree, so a
  // click in a row's menu or dialog reaches the row though it is not in it.
  if (!ev.currentTarget.contains(ev.target as Node)) return true;
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
