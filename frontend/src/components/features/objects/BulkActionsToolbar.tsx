"use client";

import { TagIcon, TrashIcon, XMarkIcon } from "@heroicons/react/24/outline";
import { cn } from "@/lib/utils";

interface BulkActionsToolbarProps {
  selectedCount: number;
  onEditLabels: () => void;
  onBulkDelete: () => void;
  onClearSelection: () => void;
  isProcessing: boolean;
}

export function BulkActionsToolbar({
  selectedCount,
  onEditLabels,
  onBulkDelete,
  onClearSelection,
  isProcessing,
}: BulkActionsToolbarProps) {
  if (selectedCount === 0) return null;

  const actions = [
    {
      id: "edit",
      label: "Sync Labels",
      icon: TagIcon,
      color: "text-indigo-400",
      hover: "hover:bg-indigo-600 hover:text-white hover:border-indigo-500",
      onClick: onEditLabels,
    },
    {
      id: "archive",
      label: "Delete",
      icon: TrashIcon,
      color: "text-rose-400",
      hover: "hover:bg-rose-600 hover:text-white hover:border-rose-500",
      onClick: onBulkDelete,
    },
  ];

  return (
    <div className="fixed bottom-10 left-1/2 -translate-x-1/2 z-[80] animate-bounce-in">
      <div className="bg-[#0A0B10]/80 backdrop-blur-3xl rounded-[32px] px-8 py-5 flex items-center gap-10 shadow-[0_30px_60px_rgba(0,0,0,0.6)] border border-white/10 ring-1 ring-white/5 relative overflow-hidden group/toolbar">
        <div className="absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-indigo-500/50 to-transparent" />

        <div className="flex items-center gap-4">
          <div className="relative">
            <div className="absolute inset-0 bg-indigo-500 rounded-2xl animate-ping opacity-20" />
            <div className="relative w-12 h-12 rounded-2xl bg-indigo-600 flex flex-col items-center justify-center text-white shadow-2xl shadow-indigo-600/40 border border-indigo-400/30">
              <span className="text-sm font-bold leading-none">
                {selectedCount}
              </span>
              <span className="text-xs font-semibold uppercase tracking-tight opacity-60">
                Units
              </span>
            </div>
          </div>
          <div className="space-y-0.5">
            <h3 className="text-xs font-bold text-white uppercase tracking-wider">
              Bulk Actions
            </h3>
            <p className="text-xs text-indigo-400/60 font-semibold uppercase tracking-wider">
              Multiple Objects Selected
            </p>
          </div>
        </div>

        <div className="h-10 w-px bg-white/10" />

        <div className="flex items-center gap-4">
          {actions.map((action) => (
            <button
              key={action.id}
              onClick={action.onClick}
              disabled={isProcessing}
              className={cn(
                "flex flex-col items-center gap-1.5 px-4 py-2 rounded-2xl transition-all border border-transparent active:scale-90 group/btn",
                action.hover,
                isProcessing && "opacity-50 cursor-not-allowed",
              )}
            >
              <action.icon
                className={cn(
                  "w-5 h-5 transition-transform group-hover/btn:scale-110",
                  action.color,
                  "group-hover/btn:text-white",
                )}
              />
              <span className="text-xs font-bold uppercase tracking-wider">
                {action.label}
              </span>
            </button>
          ))}
        </div>

        <div className="h-10 w-px bg-white/10" />

        <button
          onClick={onClearSelection}
          className="p-3 rounded-2xl bg-white/5 text-slate-500 hover:text-white hover:bg-white/10 transition-all active:scale-90"
          title="Discard Selection"
        >
          <XMarkIcon className="w-5 h-5" />
        </button>
      </div>
    </div>
  );
}
