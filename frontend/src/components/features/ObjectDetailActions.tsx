import {
  ArrowDownTrayIcon,
  ArrowPathIcon,
  EllipsisHorizontalIcon,
  PencilSquareIcon,
  ShareIcon,
  TrashIcon,
  XMarkIcon,
} from "@heroicons/react/24/outline";

import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import { Button } from "@/components/ui/button";
import { Dropdown } from "@/components/ui/Dropdown";
import { useTenantChangesBlocked } from "@/app/tenants/[id]/tenant-context";

/**
 * Header action bar for the object detail view, extracted from
 * ObjectDetailView. Presentational: edit / share / download buttons plus the
 * overflow menu (restore-or-trash depending on state, and permanent delete).
 * All behavior is delegated to the parent via callbacks; `onAction` opens the
 * parent's confirm modal.
 *
 * An object in a public collection has no trash (ADR-0027): its bytes would
 * stay served at its URL, so only a permanent delete is offered.
 */
export function ObjectDetailActions({
  state,
  onEdit,
  onShare,
  onDownload,
  onAction,
  publicObject = false,
}: {
  state: ObjectState;
  /** The object is in a public collection: no trash. */
  publicObject?: boolean;
  onEdit: () => void;
  onShare: () => void;
  onDownload: () => void;
  onAction: (action: "trash" | "purge" | "restore") => void;
}) {
  // Reads stay open on a trashed tenant; every change is held.
  const changesBlocked = useTenantChangesBlocked();
  return (
    <div className="flex items-center gap-2">
      <Button
        variant="outline"
        size="sm"
        disabled={Boolean(changesBlocked)}
        title={changesBlocked ?? undefined}
        onClick={onEdit}
      >
        <PencilSquareIcon className="size-4" />
        <span className="hidden sm:inline">Edit Metadata</span>
      </Button>
      <Button variant="outline" size="sm" onClick={onShare}>
        <ShareIcon className="size-4" />
        <span className="hidden sm:inline">Share</span>
      </Button>
      <Button size="sm" onClick={onDownload}>
        <ArrowDownTrayIcon className="size-4" />
        Download
      </Button>
      <Dropdown align="right" width="w-56">
        <Dropdown.Trigger disabled={Boolean(changesBlocked)}>
          <Button
            variant="outline"
            size="icon-sm"
            aria-label="Object actions"
            disabled={Boolean(changesBlocked)}
            title={changesBlocked ?? undefined}
          >
            <EllipsisHorizontalIcon className="size-4" />
          </Button>
        </Dropdown.Trigger>
        <Dropdown.Menu className="py-1">
          {state === ObjectState.DELETED ? (
            <Dropdown.Item onClick={() => onAction("restore")}>
              <span className="flex items-center gap-2">
                <ArrowPathIcon className="size-4" />
                Restore Object
              </span>
            </Dropdown.Item>
          ) : publicObject ? null : (
            <Dropdown.Item onClick={() => onAction("trash")}>
              <span className="flex items-center gap-2">
                <TrashIcon className="size-4" />
                Move to Trash
              </span>
            </Dropdown.Item>
          )}
          <Dropdown.Item onClick={() => onAction("purge")}>
            <span className="flex items-center gap-2 text-destructive">
              <XMarkIcon className="size-4" />
              Permanently Delete
            </span>
          </Dropdown.Item>
        </Dropdown.Menu>
      </Dropdown>
    </div>
  );
}
