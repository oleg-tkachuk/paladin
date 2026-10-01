"use client";

// ExportAuditLogDialog — kicks off an async audit-log export via
// ExportAuditLog. The RPC returns an Operation (not the bytes), so the export
// runs server-side and streams to the destination; the operator tracks
// completion in the Operations drawer. The destination is either an object
// name inside the Paladin bucket or an EventSubscription resource name to fan out
// to. The filter is a CEL expression (same grammar as the list filter).

import { useState } from "react";

import { auditClient } from "@/lib/connect/client";
import { useNotification } from "@/components/ui/Notification";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { errorMessage } from "@/hooks/errorContract";

export function ExportAuditLogDialog({
  open,
  onOpenChange,
  initialFilter = "",
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  initialFilter?: string;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Export audit log</DialogTitle>
          <DialogDescription>
            Runs server-side and streams to the destination. Track progress in
            the Operations drawer.
          </DialogDescription>
        </DialogHeader>
        {open && (
          <ExportForm
            initialFilter={initialFilter}
            onCancel={() => onOpenChange(false)}
            onDone={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function ExportForm({
  initialFilter,
  onCancel,
  onDone,
}: {
  initialFilter: string;
  onCancel: () => void;
  onDone: () => void;
}) {
  const { showNotification } = useNotification();
  const [filter, setFilter] = useState(initialFilter);
  const [destination, setDestination] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    try {
      const op = await auditClient.exportAuditLog({
        filter: filter.trim(),
        destination: destination.trim(),
      });
      showNotification({
        type: "success",
        title: "Export started",
        message: op.name || "See Operations for progress",
      });
      onDone();
    } catch (err) {
      showNotification({
        type: "error",
        title: "Could not start export",
        message: errorMessage(err),
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="space-y-3">
        <div className="space-y-1">
          <Label htmlFor="exp-dest">Destination</Label>
          <Input
            id="exp-dest"
            placeholder="audit/2026-07.ndjson or eventSubscriptions/…"
            value={destination}
            onChange={(e) => setDestination(e.target.value)}
          />
          <p className="text-[11px] text-muted-foreground">
            An object name inside the Paladin bucket, or an EventSubscription
            resource name to fan out to.
          </p>
        </div>
        <div className="space-y-1">
          <Label htmlFor="exp-filter">Filter (CEL, optional)</Label>
          <Input
            id="exp-filter"
            placeholder='action.startsWith("admin.") &amp;&amp; error != ""'
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          <p className="text-[11px] text-muted-foreground">
            Blank exports everything. Same grammar as the list filter.
          </p>
        </div>
      </div>
      <DialogFooter>
        <Button variant="ghost" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
        <Button
          onClick={() => void submit()}
          disabled={busy || destination.trim() === ""}
        >
          {busy ? "Starting…" : "Start export"}
        </Button>
      </DialogFooter>
    </>
  );
}
