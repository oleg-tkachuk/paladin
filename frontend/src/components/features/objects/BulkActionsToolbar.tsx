"use client";

import { TagIcon, TrashIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { SelectionBar } from "@/components/ui/SelectionBar";
import { useTenantChangesBlocked } from "@/app/tenants/[id]/tenant-context";

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
  const changesBlocked = useTenantChangesBlocked();
  const held = isProcessing || Boolean(changesBlocked);
  return (
    <SelectionBar count={selectedCount} onClear={onClearSelection}>
      <Button
        size="sm"
        variant="ghost"
        disabled={held}
        title={changesBlocked ?? undefined}
        onClick={onEditLabels}
      >
        <TagIcon />
        Sync Labels
      </Button>
      <Button
        size="sm"
        variant="ghost"
        className="text-destructive hover:bg-destructive/10 hover:text-destructive"
        disabled={held}
        title={changesBlocked ?? undefined}
        onClick={onBulkDelete}
      >
        <TrashIcon />
        Delete
      </Button>
    </SelectionBar>
  );
}
