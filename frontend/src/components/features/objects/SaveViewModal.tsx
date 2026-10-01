"use client";

interface SaveViewModalProps {
  isOpen: boolean;
  viewName: string;
  onViewNameChange: (value: string) => void;
  onSave: () => void;
  onClose: () => void;
}

export function SaveViewModal({
  isOpen,
  viewName,
  onViewNameChange,
  onSave,
  onClose,
}: SaveViewModalProps) {
  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-[110] flex items-center justify-center p-4 animate-fade-in">
      <div
        className="fixed inset-0 bg-black/60 backdrop-blur-md"
        onClick={onClose}
      />
      <div className="relative w-full max-w-sm bg-background rounded-[32px] border border-border shadow-2xl p-8 space-y-6 overflow-hidden">
        <div className="absolute top-0 left-0 w-full h-1 bg-primary" />
        <div className="space-y-2">
          <h3 className="text-xl font-bold text-foreground uppercase tracking-tight">
            Save View Configuration
          </h3>
          <p className="text-xs text-muted-foreground font-medium">
            Create a bookmark for these specific filters.
          </p>
        </div>

        <div className="space-y-4">
          <label className="text-xs font-semibold text-primary uppercase tracking-wider">
            View Name
          </label>
          <input
            autoFocus
            type="text"
            className="w-full bg-background/40 border border-border rounded-2xl px-4 py-3 text-foreground text-sm focus:border-primary/50 outline-none transition-all"
            placeholder="e.g. Production Assets"
            value={viewName}
            onChange={(e) => onViewNameChange(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && onSave()}
          />
        </div>

        <div className="flex gap-4 pt-2">
          <button
            onClick={onSave}
            className="flex-1 py-3.5 rounded-2xl bg-primary text-primary-foreground text-xs font-bold uppercase tracking-wider hover:bg-primary transition-all active:scale-95"
          >
            Save View
          </button>
          <button
            onClick={onClose}
            className="px-6 py-3.5 rounded-2xl bg-foreground/5 text-muted-foreground hover:text-foreground text-xs font-bold uppercase tracking-wider transition-all"
          >
            Abort
          </button>
        </div>
      </div>
    </div>
  );
}
