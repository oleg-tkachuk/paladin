"use client";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";

interface BulkEditModalProps {
  isOpen: boolean;
  selectedCount: number;
  labels: string;
  onLabelsChange: (value: string) => void;
  onSubmit: () => void;
  onClose: () => void;
  isProcessing: boolean;
}

// Built on the shadcn/Radix Dialog so the overlay carries the full
// dialog contract (role="dialog", aria-modal, focus trap, Escape and
// overlay-click to close) — the previous hand-rolled div had none.
export function BulkEditModal({
  isOpen,
  selectedCount,
  labels,
  onLabelsChange,
  onSubmit,
  onClose,
  isProcessing,
}: BulkEditModalProps) {
  return (
    <Dialog open={isOpen} onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        showCloseButton={false}
        className="max-w-lg bg-[#0A0C10] rounded-[32px] border border-white/10 shadow-2xl p-8 gap-6 overflow-hidden"
      >
        <div className="absolute top-0 left-0 w-full h-1 bg-primary" />
        <div className="space-y-2">
          <DialogTitle className="text-xl font-bold text-white uppercase tracking-tight">
            Bulk Label Synchronization
          </DialogTitle>
          <DialogDescription className="text-xs text-muted-foreground font-medium">
            Updating {selectedCount} resources simultaneously.
          </DialogDescription>
        </div>

        <div className="space-y-4">
          <label className="text-xs font-semibold text-primary uppercase tracking-wider">
            New Object Tags
            <textarea
              autoFocus
              className="mt-2 w-full h-32 bg-black/40 border border-white/10 rounded-2xl p-4 text-white text-sm font-mono focus:border-primary/50 outline-none transition-all resize-none normal-case tracking-normal font-normal"
              placeholder="key:value, key2:value2..."
              value={labels}
              onChange={(e) => onLabelsChange(e.target.value)}
            />
          </label>
          <p className="text-xs text-muted-foreground italic">
            Example: environment:production, department:engineering
          </p>
        </div>

        <div className="flex gap-4 pt-4">
          <button
            onClick={onSubmit}
            disabled={isProcessing}
            className="flex-1 py-4 rounded-2xl bg-primary text-primary-foreground text-xs font-bold uppercase tracking-wider hover:bg-primary transition-all shadow-xl shadow-primary/20 active:scale-95"
          >
            {isProcessing ? "Syncing..." : "Update Tags"}
          </button>
          <button
            onClick={onClose}
            className="px-8 py-4 rounded-2xl bg-white/5 text-muted-foreground hover:text-white text-xs font-bold uppercase tracking-wider transition-all border border-white/5"
          >
            Abort
          </button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
