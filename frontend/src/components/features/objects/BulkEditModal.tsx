"use client";

interface BulkEditModalProps {
  isOpen: boolean;
  selectedCount: number;
  labels: string;
  onLabelsChange: (value: string) => void;
  onSubmit: () => void;
  onClose: () => void;
  isProcessing: boolean;
}

export function BulkEditModal({
  isOpen,
  selectedCount,
  labels,
  onLabelsChange,
  onSubmit,
  onClose,
  isProcessing,
}: BulkEditModalProps) {
  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center p-4 animate-fade-in">
      <div
        className="fixed inset-0 bg-black/60 backdrop-blur-md"
        onClick={onClose}
      />
      <div className="relative w-full max-w-lg bg-[#0A0C10] rounded-[32px] border border-white/10 shadow-2xl p-8 space-y-6 overflow-hidden">
        <div className="absolute top-0 left-0 w-full h-1 bg-indigo-500" />
        <div className="space-y-2">
          <h3 className="text-xl font-bold text-white uppercase tracking-tight">
            Bulk Label Synchronization
          </h3>
          <p className="text-xs text-slate-500 font-medium">
            Updating {selectedCount} resources simultaneously.
          </p>
        </div>

        <div className="space-y-4">
          <label className="text-xs font-semibold text-indigo-400 uppercase tracking-wider">
            New Object Tags
          </label>
          <textarea
            autoFocus
            className="w-full h-32 bg-black/40 border border-white/10 rounded-2xl p-4 text-white text-sm font-mono focus:border-indigo-500/50 outline-none transition-all resize-none"
            placeholder="key:value, key2:value2..."
            value={labels}
            onChange={(e) => onLabelsChange(e.target.value)}
          />
          <p className="text-xs text-slate-600 italic">
            Example: environment:production, department:engineering
          </p>
        </div>

        <div className="flex gap-4 pt-4">
          <button
            onClick={onSubmit}
            disabled={isProcessing}
            className="flex-1 py-4 rounded-2xl bg-indigo-600 text-white text-xs font-bold uppercase tracking-wider hover:bg-indigo-500 transition-all shadow-xl shadow-indigo-600/20 active:scale-95"
          >
            {isProcessing ? "Syncing..." : "Update Tags"}
          </button>
          <button
            onClick={onClose}
            className="px-8 py-4 rounded-2xl bg-white/5 text-slate-500 hover:text-white text-xs font-bold uppercase tracking-wider transition-all border border-white/5"
          >
            Abort
          </button>
        </div>
      </div>
    </div>
  );
}
