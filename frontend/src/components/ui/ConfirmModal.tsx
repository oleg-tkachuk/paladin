"use client";

import React from "react";
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
  message: string;
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
  type = "warning",
  confirmText = "Confirm",
  cancelText = "Cancel",
  loading = false,
}: ConfirmModalProps) {
  const handleConfirm = async () => {
    await onConfirm();
    onClose();
  };

  const isDanger = type === "danger";

  return (
    <AlertDialog open={isOpen} onOpenChange={(o) => !o && onClose()}>
      <AlertDialogContent className="max-w-sm rounded-[2rem] border border-border bg-popover shadow-2xl p-8 gap-6">
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
            <AlertDialogDescription className="text-sm text-muted-foreground mt-2 leading-relaxed">
              {message}
            </AlertDialogDescription>
          </div>
        </div>
        <div className="flex gap-3">
          <button
            onClick={onClose}
            disabled={loading}
            className="flex-1 px-4 py-2.5 rounded-xl bg-muted/50 border border-border text-sm font-semibold text-foreground hover:bg-muted transition-all disabled:opacity-50"
          >
            {cancelText}
          </button>
          <button
            onClick={handleConfirm}
            disabled={loading}
            className={cn(
              "flex-1 px-4 py-2.5 rounded-xl text-sm font-semibold transition-all active:scale-[0.98] disabled:opacity-50",
              isDanger
                ? "bg-destructive text-destructive-foreground hover:bg-destructive/90"
                : "bg-warning text-warning-foreground hover:bg-warning/90",
            )}
          >
            {loading ? "Processing..." : confirmText}
          </button>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  );
}
