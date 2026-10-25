"use client";

import { useId, useRef, useState, type ReactNode } from "react";
import {
  ExclamationTriangleIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Input } from "@/components/ui/input";
import { errorMessage } from "@/hooks/errorContract";
import { formatCount } from "@/lib/format/locale";
import { cn } from "@/lib/utils";
import { PRESSED } from "@/lib/ui/states";

/** What the dialog says when the action failed and gave no reason. */
export const CONFIRM_FAILED = "The action failed.";

/** How many affected names are listed before the rest become a count. */
const AFFECTED_SHOWN = 5;

/**
 * What an action removes or changes, listed in the dialog so the operator
 * reads the names rather than a number: the backends a bulk disable reaches,
 * the objects a purge destroys.
 */
export interface Affected {
  /** The list's heading, with its count: "3 backends", "Removed with it". */
  heading: string;
  /** The names, in the order shown; only the first few are listed. */
  items: readonly string[];
  /**
   * How many there are, when the names given are not all of them; the rest
   * are counted in "and N more".
   */
  total?: number;
  /** Names are set in monospace, as identifiers are; off for prose. */
  mono?: boolean;
}

interface ConfirmModalProps {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: () => void | Promise<void>;
  title: string;
  message: ReactNode;
  // What the action does that the operator might not expect, shown apart from
  // the message so it is not skimmed past.
  warning?: ReactNode;
  // A control the action takes an option from, such as a checkbox; shown
  // between the warning and the buttons.
  children?: ReactNode;
  type?: "danger" | "warning";
  confirmText?: string;
  // Holds the confirm button while the action's own input is not ready, such
  // as an empty field the action needs.
  confirmDisabled?: boolean;
  cancelText?: string;
  loading?: boolean;
  // The exact text the operator types before the confirm button enables, as
  // Argo CD asks for an application's name before deleting it. Given for an
  // action nothing in the console can undo: a purge, a delete with no trash,
  // a credential's revoke. It is the target's name where it has one an
  // operator knows it by (a slug, a bucket, a Collection, a backend, a user),
  // and the action's verb where it does not (an object key, an opaque id, a
  // selection): pasting an id copied from the row proves nothing, and a
  // selection's count changes as rows are ticked.
  typeToConfirm?: string;
  // What the action reaches, listed by name; see Affected. Several lists
  // show one under another, in the order given.
  affected?: Affected | readonly Affected[];
}

// Colours are theme tokens, like every other dialog's: the fixed dark hex and
// slate/rose/amber palette it used would not follow a theme change.
//
// Built on the Radix AlertDialog primitive so confirmation prompts get
// the full dialog contract for free: role="alertdialog", aria wiring,
// focus trap, Escape-to-cancel, scroll lock. The previous hand-rolled
// div overlay had none of those.
export function ConfirmModal({
  isOpen,
  onClose,
  onConfirm,
  title,
  message,
  warning,
  children,
  type = "warning",
  confirmText = "Confirm",
  confirmDisabled = false,
  cancelText = "Cancel",
  loading = false,
  typeToConfirm,
  affected,
}: ConfirmModalProps) {
  // Held while onConfirm runs, so a second click cannot start the action
  // twice even when the caller passes no loading state of its own.
  const [running, setRunning] = useState(false);
  const busy = loading || running;
  // Why the action failed, kept against the action it belongs to: the
  // dialog stays open on a refusal and says it, where it used to sit there
  // unchanged while the error went unhandled. A different action reopening
  // the dialog does not inherit an old reason.
  const [failure, setFailure] = useState<{
    action: ConfirmModalProps["onConfirm"];
    reason: string;
  } | null>(null);
  const reason = failure?.action === onConfirm ? failure.reason : null;

  // What the operator has typed, emptied whenever the dialog opens, closes or
  // names another target: a match typed for one bucket must not stand for
  // the next. Adjusted during render rather than in an effect, so no frame
  // shows the old text holding the button enabled.
  const [typed, setTyped] = useState("");
  const [typedFor, setTypedFor] = useState({ isOpen, typeToConfirm });
  if (typedFor.isOpen !== isOpen || typedFor.typeToConfirm !== typeToConfirm) {
    setTypedFor({ isOpen, typeToConfirm });
    setTyped("");
  }
  // Exact and case-sensitive, as the names are. Only the whitespace a paste
  // brings along at either end is let go, since no name begins or ends in it.
  const typedMatches =
    typeToConfirm === undefined || typed.trim() === typeToConfirm;
  const typedRef = useRef<HTMLInputElement>(null);
  const typedId = useId();
  const canConfirm = !busy && !confirmDisabled && typedMatches;

  const handleConfirm = async () => {
    setRunning(true);
    setFailure(null);
    try {
      await onConfirm();
      onClose();
    } catch (err) {
      setFailure({
        action: onConfirm,
        reason: errorMessage(err, CONFIRM_FAILED),
      });
    } finally {
      setRunning(false);
    }
  };

  const isDanger = type === "danger";

  return (
    <AlertDialog open={isOpen} onOpenChange={(o) => !o && onClose()}>
      {/* Above every panel and bar it can be opened from — the object
          inspector (z-60), a selection bar (z-50) — so none covers the
          question it raised. */}
      <AlertDialogContent
        overlayClassName="z-100"
        className="z-100 max-w-sm rounded-[2rem] border border-border bg-popover shadow-overlay p-8 gap-6"
        // The typed field is what has to be filled before anything else
        // works, so it takes the focus; otherwise the first control would,
        // which may be an option the action takes.
        onOpenAutoFocus={(e) => {
          if (!typedRef.current) return;
          e.preventDefault();
          typedRef.current.focus();
        }}
      >
        <div className="flex flex-col items-center text-center space-y-4">
          <div
            className={cn(
              "w-14 h-14 rounded-2xl flex items-center justify-center",
              isDanger
                ? "bg-destructive/10 text-destructive"
                : "bg-warning/10 text-warning",
            )}
          >
            {isDanger ? (
              <TrashIcon className="w-7 h-7" />
            ) : (
              <ExclamationTriangleIcon className="w-7 h-7" />
            )}
          </div>
          <div>
            <AlertDialogTitle className="text-lg font-bold text-popover-foreground">
              {title}
            </AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="text-sm text-muted-foreground mt-2 leading-relaxed">
                {message}
              </div>
            </AlertDialogDescription>
          </div>
        </div>
        {warning && (
          <div
            role="note"
            className={cn(
              "flex items-start gap-2 rounded-xl px-3 py-2.5 text-left text-xs leading-relaxed",
              isDanger
                ? "bg-destructive/10 text-destructive"
                : "bg-warning/10 text-warning",
            )}
          >
            <ExclamationTriangleIcon className="mt-px size-4 shrink-0" />
            <div>{warning}</div>
          </div>
        )}
        {affectedLists(affected).map((a) => (
          <AffectedList key={a.heading} {...a} />
        ))}
        {children}
        {typeToConfirm !== undefined && (
          <div className="space-y-1.5 text-left">
            <label
              htmlFor={typedId}
              className="block text-xs leading-relaxed text-muted-foreground"
            >
              Type{" "}
              <span className="font-mono font-semibold break-all text-foreground">
                {typeToConfirm}
              </span>{" "}
              to confirm
            </label>
            <Input
              ref={typedRef}
              id={typedId}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              // Enter confirms only once the text matches; before that it
              // does nothing, rather than acting on a half-typed name.
              onKeyDown={(e) => {
                if (e.key !== "Enter") return;
                e.preventDefault();
                if (canConfirm) void handleConfirm();
              }}
              aria-describedby={`${typedId}-hint`}
              autoComplete="off"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              className="font-mono"
            />
            <p id={`${typedId}-hint`} className="text-xs text-muted-foreground">
              It has to match exactly, capitals included.
            </p>
          </div>
        )}
        {reason && (
          <p
            role="alert"
            className="rounded-xl bg-destructive/10 px-3 py-2.5 text-left text-xs leading-relaxed text-destructive"
          >
            {reason}
          </p>
        )}
        <div className="flex gap-3">
          <button
            onClick={onClose}
            disabled={busy}
            className="flex-1 px-4 py-2.5 rounded-xl bg-muted/50 border border-border text-sm font-semibold text-foreground hover:bg-muted transition-all disabled:opacity-50"
          >
            {cancelText}
          </button>
          <button
            onClick={handleConfirm}
            disabled={!canConfirm}
            className={cn(
              "flex-1 px-4 py-2.5 rounded-xl text-sm font-semibold transition-all disabled:opacity-50",
              PRESSED,
              isDanger
                ? "bg-destructive text-destructive-foreground hover:bg-destructive/90"
                : "bg-warning text-warning-foreground hover:bg-warning/90",
            )}
          >
            {busy ? "Processing..." : confirmText}
          </button>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  );
}

// The names an action reaches, a few at most and then how many more: enough
// to recognise the selection by, without the dialog outgrowing the screen.
// affectedLists is affected as a list of lists, empty when there is none.
function affectedLists(
  affected: Affected | readonly Affected[] | undefined,
): readonly Affected[] {
  if (!affected) return [];
  return Array.isArray(affected) ? affected : [affected as Affected];
}

function AffectedList({ heading, items, total, mono = true }: Affected) {
  const headingId = useId();
  const shown = items.slice(0, AFFECTED_SHOWN);
  const more = Math.max(total ?? 0, items.length) - shown.length;
  return (
    <div className="rounded-xl border border-border bg-muted/30 px-3 py-2.5 text-left">
      <p id={headingId} className="text-xs font-semibold text-foreground">
        {heading}
      </p>
      <ul
        aria-labelledby={headingId}
        className={cn(
          "mt-1.5 space-y-0.5 text-xs text-muted-foreground",
          mono && "font-mono",
        )}
      >
        {/* An identifier is cut to one line, whole on hover, so a long
            object key does not stretch the dialog; prose wraps. */}
        {shown.map((item, i) => (
          <li
            key={`${i}:${item}`}
            className={mono ? "truncate" : undefined}
            title={mono ? item : undefined}
          >
            {item}
          </li>
        ))}
        {more > 0 && (
          <li className="font-sans tabular-nums">
            and {formatCount(more)} more
          </li>
        )}
      </ul>
    </div>
  );
}
