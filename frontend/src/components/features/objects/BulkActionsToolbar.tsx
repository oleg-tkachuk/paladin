"use client";

import {
  ArrowUturnLeftIcon,
  DocumentDuplicateIcon,
  TagIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { SelectionBar } from "@/components/ui/SelectionBar";
import { useTenantChangesBlocked } from "@/app/tenants/[id]/tenant-context";

interface BulkActionsToolbarProps {
  selectedCount: number;
  onEditLabels: () => void;
  onBulkDelete: () => void;
  /** Offered when every selected object is in the trash. */
  onBulkRestore?: () => void;
  /** Offered where objects may be copied. */
  onBulkCopy?: () => void;
  onClearSelection: () => void;
  isProcessing: boolean;
}

export function BulkActionsToolbar({
  selectedCount,
  onEditLabels,
  onBulkDelete,
  onBulkRestore,
  onBulkCopy,
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
      {onBulkCopy ? (
        <Button
          size="sm"
          variant="ghost"
          disabled={held}
          title={changesBlocked ?? undefined}
          onClick={onBulkCopy}
        >
          <DocumentDuplicateIcon />
          Copy
        </Button>
      ) : null}
      {onBulkRestore ? (
        <Button
          size="sm"
          variant="ghost"
          disabled={held}
          title={changesBlocked ?? undefined}
          onClick={onBulkRestore}
        >
          <ArrowUturnLeftIcon />
          Restore
        </Button>
      ) : null}
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
