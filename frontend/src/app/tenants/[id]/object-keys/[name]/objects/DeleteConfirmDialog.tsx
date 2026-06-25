"use client";

// Soft / hard delete confirmation for the objects page, extracted from the
// inline AlertDialog. Driven by the page's `confirm` state machine: the page
// supplies the copy + the danger/warning styling and an onConfirm that runs
// the queued action then closes. Controlled open via onOpenChange.
import {
  ExclamationTriangleIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

export function DeleteConfirmDialog({
  open,
  type,
  title,
  message,
  confirmText,
  onConfirm,
  onOpenChange,
}: {
  open: boolean;
  type: "danger" | "warning";
  title: string;
  message: string;
  confirmText: string;
  onConfirm: () => void;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle className="flex items-center gap-2">
            {type === "danger" ? (
              <ExclamationTriangleIcon className="size-5 text-destructive" />
            ) : (
              <TrashIcon className="size-5 text-chart-3" />
            )}
            {title}
          </AlertDialogTitle>
          <AlertDialogDescription className="whitespace-pre-wrap">
            {message}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={onConfirm}
            className={
              type === "danger"
                ? "bg-destructive text-destructive-foreground hover:bg-destructive/90"
                : ""
            }
          >
            {confirmText}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
