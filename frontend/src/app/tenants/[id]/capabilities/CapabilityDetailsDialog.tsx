"use client";

import { ClipboardDocumentIcon } from "@heroicons/react/24/outline";

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
import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";

import { DetailsBody } from "./_details";

type UsageEntry =
  | { requestCount: bigint; spentAmount: number; unitCode: string }
  | "never"
  | undefined;

/**
 * Read-only capability details dialog, extracted from the capabilities page.
 * No RPC — renders the row data + the usage entry the page already fetched.
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
            <DetailsBody cap={cap} usageEntry={usageEntry} />
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
