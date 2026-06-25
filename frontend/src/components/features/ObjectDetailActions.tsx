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

/**
 * Header action bar for the object detail view, extracted from
 * ObjectDetailView. Presentational: edit / share / download buttons plus the
 * overflow menu (restore-or-trash depending on state, and permanent delete).
 * All behavior is delegated to the parent via callbacks; `onAction` opens the
 * parent's confirm modal.
 */
export function ObjectDetailActions({
  state,
  onEdit,
  onShare,
  onDownload,
  onAction,
}: {
  state: ObjectState;
  onEdit: () => void;
  onShare: () => void;
  onDownload: () => void;
  onAction: (action: "trash" | "purge" | "restore") => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <Button variant="outline" size="sm" onClick={onEdit}>
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
        <Dropdown.Trigger>
          <Button variant="outline" size="icon-sm" aria-label="Object actions">
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
          ) : (
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
