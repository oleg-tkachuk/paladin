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
      <AlertDialogContent className="max-w-sm bg-[#0A0C10] rounded-[2rem] border border-white/10 shadow-2xl p-8 gap-6">
        <div className="flex flex-col items-center text-center space-y-4">
          <div
            className={cn(
              "w-14 h-14 rounded-2xl flex items-center justify-center",
              isDanger
                ? "bg-rose-500/10 text-rose-500"
                : "bg-amber-500/10 text-amber-500",
            )}
          >
            {isDanger ? (
              <TrashIcon className="w-7 h-7" />
            ) : (
              <ExclamationTriangleIcon className="w-7 h-7" />
            )}
          </div>
          <div>
            <AlertDialogTitle className="text-lg font-bold text-white">
              {title}
            </AlertDialogTitle>
            <AlertDialogDescription className="text-sm text-slate-400 mt-2 leading-relaxed">
              {message}
            </AlertDialogDescription>
          </div>
        </div>
        <div className="flex gap-3">
          <button
            onClick={onClose}
            disabled={loading}
            className="flex-1 px-4 py-2.5 rounded-xl bg-white/5 border border-white/10 text-sm font-semibold text-slate-300 hover:bg-white/10 transition-all disabled:opacity-50"
          >
            {cancelText}
          </button>
          <button
            onClick={handleConfirm}
            disabled={loading}
            className={cn(
              "flex-1 px-4 py-2.5 rounded-xl text-sm font-semibold text-white transition-all active:scale-[0.98] disabled:opacity-50",
              isDanger
                ? "bg-rose-600 hover:bg-rose-500 shadow-lg shadow-rose-600/20"
                : "bg-amber-600 hover:bg-amber-500 shadow-lg shadow-amber-600/20",
            )}
          >
            {loading ? "Processing..." : confirmText}
          </button>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  );
}
