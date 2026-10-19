"use client";

import { ClipboardDocumentIcon } from "@heroicons/react/24/outline";
import { useQuery } from "@tanstack/react-query";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useNotification } from "@/components/ui/Notification";
import { copyToClipboard } from "@/lib/utils";
import { capabilityDetailQuery } from "@/lib/queries/details";
import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import type { Money } from "@/gen/google/type/money_pb";

import { DetailsBody, type RecordEntry } from "./_details";

type UsageEntry =
  { requestCount: bigint; spent: Money | undefined } | "never" | undefined;

/**
 * Read-only capability details dialog, extracted from the capabilities page.
 * Renders the row data and the usage entry the page already fetched, and
 * reads the record (CapabilityService.Get) for who issued the capability and
 * why it was revoked, which the list does not carry.
 */
export function CapabilityDetailsDialog({
  id,
  cap,
  usageEntry,
  onClose,
}: {
  /** The capability to show; null keeps the dialog closed. */
  id: string | null;
  /** Its row from the page's list, shown before the record answers. */
  cap: Capability | null;
  usageEntry: UsageEntry;
  onClose: () => void;
}) {
  const { showNotification } = useNotification();
  const recordQuery = useQuery({
    ...capabilityDetailQuery(id ?? ""),
    enabled: !!id,
  });
  // A capability linked by id may not be in the list the page browsed.
  const shown = cap ?? recordQuery.data?.capability ?? null;
  const record: RecordEntry = recordQuery.isError
    ? "unavailable"
    : recordQuery.data;

  return (
    <Dialog open={!!id} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-[calc(100%-2rem)] sm:max-w-5xl">
        {shown ? (
          <>
            <DialogHeader>
              <DialogTitle>Capability details</DialogTitle>
              <DialogDescription>
                Full record as stored on the admin plane.
              </DialogDescription>
            </DialogHeader>
            <DetailsBody cap={shown} usageEntry={usageEntry} record={record} />
            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  void copyToClipboard(shown.id);
                  showNotification({
                    type: "success",
                    title: "Copied",
                    message: "Capability ID copied.",
                  });
                }}
              >
                <ClipboardDocumentIcon className="size-4" />
                Copy ID
              </Button>
              <Button onClick={onClose}>Close</Button>
            </DialogFooter>
          </>
        ) : (
          <DialogHeader>
            <DialogTitle>Capability details</DialogTitle>
            <DialogDescription>
              {recordQuery.isError
                ? `No capability ${id} is on record, or it is not visible to you.`
                : "Reading the capability…"}
            </DialogDescription>
          </DialogHeader>
        )}
      </DialogContent>
    </Dialog>
  );
}
