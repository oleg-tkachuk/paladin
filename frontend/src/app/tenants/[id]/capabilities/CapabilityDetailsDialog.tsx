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
import { capabilityClient } from "@/lib/connect/client";
import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";

import { DetailsBody, type RecordEntry } from "./_details";

type UsageEntry =
  | { requestCount: bigint; spentAmount: number; unitCode: string }
  | "never"
  | undefined;

/**
 * Read-only capability details dialog, extracted from the capabilities page.
 * Renders the row data and the usage entry the page already fetched, and
 * reads the record (CapabilityService.Get) for who issued the capability and
 * why it was revoked, which the list does not carry.
 */
export function CapabilityDetailsDialog({
  cap,
  usageEntry,
  onClose,
}: {
  cap: Capability | null;
  usageEntry: UsageEntry;
  onClose: () => void;
}) {
  const { showNotification } = useNotification();
  const recordQuery = useQuery({
    queryKey: ["capability", "record", cap?.id],
    queryFn: ({ signal }) => capabilityClient.get({ id: cap!.id }, { signal }),
    enabled: !!cap,
  });
  const record: RecordEntry = recordQuery.isError
    ? "unavailable"
    : recordQuery.data;

  return (
    <Dialog open={!!cap} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-[calc(100%-2rem)] sm:max-w-5xl">
        {cap && (
          <>
            <DialogHeader>
              <DialogTitle>Capability details</DialogTitle>
              <DialogDescription>
                Full record as stored on the admin plane.
              </DialogDescription>
            </DialogHeader>
            <DetailsBody cap={cap} usageEntry={usageEntry} record={record} />
            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  void copyToClipboard(cap.id);
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
        )}
      </DialogContent>
    </Dialog>
  );
}
