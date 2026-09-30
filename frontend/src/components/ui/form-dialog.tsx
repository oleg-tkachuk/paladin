"use client";

/**
 * The console's form dialogs — create and edit — built one way.
 *
 * Each dialog used to compose its own: a 384px box, a paragraph of prose above
 * the first field, selects as wide as their text, an "Advanced" <details>, and
 * a submit button that went grey with nothing saying why. These pieces make
 * the layout a decision taken once:
 *
 *   FormDialog      title, one-line description, a <form> so Enter submits,
 *                   a body that scrolls, and a footer that names what is
 *                   missing when the primary action is held.
 *   FormSection     a titled group of fields.
 *   FormRow         two fields side by side, each allowed to shrink.
 *   FormField       label, required marker, control, hint or error — and the
 *                   id that ties them, so a label always reaches its control.
 *   FormDisclosure  the collapsed "Advanced" group.
 */

import { useId, useState, type ReactNode } from "react";
import { ChevronRightIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/** Dialog widths, by how much a form holds. */
export const FORM_DIALOG_WIDTH = {
  /** A handful of fields, no side-by-side rows. */
  md: "sm:max-w-lg",
  /** Sections and two-column rows. */
  lg: "sm:max-w-2xl",
} as const;

export type FormDialogWidth = keyof typeof FORM_DIALOG_WIDTH;

interface FormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  /** One line. Anything longer belongs in a field's hint. */
  description?: string;
  width?: FormDialogWidth;
  onSubmit: () => void;
  submitLabel: string;
  /** Shown on the button while `submitting`. */
  submittingLabel?: string;
  submitting?: boolean;
  /**
   * Why the primary action is held, or null when it is not. Shown beside the
   * button, so a disabled submit always says what it is waiting for.
   */
  blockedReason?: string | null;
  /** A failure from the last submit, shown above the footer. */
  error?: string | null;
  children: ReactNode;
}

export function FormDialog({
  open,
  onOpenChange,
  title,
  description,
  width = "md",
  onSubmit,
  submitLabel,
  submittingLabel,
  submitting = false,
  blockedReason = null,
  error = null,
  children,
}: FormDialogProps) {
  const held = submitting || blockedReason !== null;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className={cn("gap-0 p-0", FORM_DIALOG_WIDTH[width])}>
        <form
          noValidate
          className="flex max-h-[85vh] flex-col"
          onSubmit={(e) => {
            e.preventDefault();
            if (!held) onSubmit();
          }}
        >
          <DialogHeader className="border-b px-5 pt-5 pb-4">
            <DialogTitle>{title}</DialogTitle>
            {description ? (
              <DialogDescription>{description}</DialogDescription>
            ) : null}
          </DialogHeader>

          <div className="min-h-0 flex-1 space-y-6 overflow-y-auto px-5 py-5">
            {children}
            {error ? (
              <p
                role="alert"
                className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive"
              >
                {error}
              </p>
            ) : null}
          </div>

          <DialogFooter className="m-0 items-center rounded-b-xl px-5 py-3 sm:justify-between">
            <p
              className="text-xs text-muted-foreground"
              data-slot="form-blocked-reason"
            >
              {submitting ? "" : (blockedReason ?? "")}
            </p>
            <div className="flex flex-col-reverse gap-2 sm:flex-row">
              <Button
                type="button"
                variant="ghost"
                onClick={() => onOpenChange(false)}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={held}>
                {submitting ? (submittingLabel ?? submitLabel) : submitLabel}
              </Button>
            </div>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function FormSection({
  title,
  children,
}: {
  title?: string;
  children: ReactNode;
}) {
  return (
    <section className="space-y-4">
      {title ? (
        <h3 className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
          {title}
        </h3>
      ) : null}
      {children}
    </section>
  );
}

export function FormRow({ children }: { children: ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 [&>*]:min-w-0">
      {children}
    </div>
  );
}

/** What a FormField hands its control. */
export interface FormControlProps {
  id: string;
  "aria-describedby"?: string;
  "aria-invalid"?: true;
}

interface FormFieldProps {
  label: string;
  required?: boolean;
  /** Always-on guidance. Replaced by `error` while there is one. */
  hint?: ReactNode;
  error?: string | null;
  /** Something small beside the label — a "valid" tick, a counter. */
  aside?: ReactNode;
  /** Pin the id when another element must address the control. */
  id?: string;
  children: (control: FormControlProps) => ReactNode;
}

export function FormField({
  label,
  required = false,
  hint,
  error = null,
  aside,
  id,
  children,
}: FormFieldProps) {
  const generated = useId();
  const controlId = id ?? generated;
  const noteId = `${controlId}-note`;
  const note = error ?? hint;
  return (
    // Selects size to their text by default; inside a field they fill it.
    <div className="space-y-1.5 [&_[data-slot=select-trigger]]:w-full">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={controlId}>
          {label}
          {required ? (
            <span aria-hidden className="text-destructive">
              *
            </span>
          ) : null}
        </Label>
        {aside}
      </div>
      {children({
        id: controlId,
        "aria-describedby": note ? noteId : undefined,
        "aria-invalid": error ? true : undefined,
      })}
      {note ? (
        <p
          id={noteId}
          className={cn(
            "text-xs",
            error ? "text-destructive" : "text-muted-foreground",
          )}
        >
          {note}
        </p>
      ) : null}
    </div>
  );
}

export function FormDisclosure({
  title,
  defaultOpen = false,
  children,
}: {
  title: string;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className="rounded-md border">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-medium text-muted-foreground hover:text-foreground"
      >
        <ChevronRightIcon
          className={cn("size-3.5 transition-transform", open && "rotate-90")}
        />
        {title}
      </button>
      {open ? (
        <div className="space-y-4 border-t px-3 py-4">{children}</div>
      ) : null}
    </div>
  );
}
