"use client";

import { useState, type ReactNode } from "react";
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
import { cn } from "@/lib/utils";

interface ConfirmModalProps {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: () => void | Promise<void>;
  title: string;
  message: ReactNode;
  // What the action does that the operator might not expect, shown apart from
  // the message so it is not skimmed past.
  warning?: ReactNode;
  type?: "danger" | "warning";
  confirmText?: string;
  cancelText?: string;
  loading?: boolean;
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
  type = "warning",
  confirmText = "Confirm",
  cancelText = "Cancel",
  loading = false,
}: ConfirmModalProps) {
  // Held while onConfirm runs, so a second click cannot start the action
  // twice even when the caller passes no loading state of its own.
  const [running, setRunning] = useState(false);
  const busy = loading || running;

  const handleConfirm = async () => {
    setRunning(true);
    try {
      await onConfirm();
      onClose();
    } finally {
      setRunning(false);
    }
  };

  const isDanger = type === "danger";

  return (
    <AlertDialog open={isOpen} onOpenChange={(o) => !o && onClose()}>
      {/* Above every panel and bar it can be opened from — the object
          inspector (z-60), the bulk-actions bar (z-80) — so neither covers
          the question it raised. */}
      <AlertDialogContent
        overlayClassName="z-100"
        className="z-100 max-w-sm rounded-[2rem] border border-border bg-popover shadow-2xl p-8 gap-6"
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
            disabled={busy}
            className={cn(
              "flex-1 px-4 py-2.5 rounded-xl text-sm font-semibold transition-all active:scale-[0.98] disabled:opacity-50",
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
