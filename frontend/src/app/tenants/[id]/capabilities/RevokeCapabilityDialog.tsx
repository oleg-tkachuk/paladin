"use client";

import { useState } from "react";
import { ConnectError } from "@connectrpc/connect";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useNotification } from "@/components/ui/Notification";
import { capabilityClient } from "@/lib/connect/client";
import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";

/**
 * Revoke-capability confirmation, extracted from the capabilities page. Owns
 * the reason + cascade inputs and the revoke RPC; the page refreshes the list
 * via onRevoked and clears its target via onClose.
 */
export function RevokeCapabilityDialog({
  cap,
  onClose,
  onRevoked,
}: {
  cap: Capability | null;
  onClose: () => void;
  onRevoked: () => void;
}) {
  const { showNotification } = useNotification();
  const [revokeCascade, setRevokeCascade] = useState(false);
  const [revokeReason, setRevokeReason] = useState("");

  const reset = () => {
    setRevokeCascade(false);
    setRevokeReason("");
  };
  const handleClose = () => {
    reset();
    onClose();
  };

  const handleRevoke = async () => {
    if (!cap) return;
    try {
      await capabilityClient.revoke({
        id: cap.id,
        reason: revokeReason.trim(),
        cascadeChildren: revokeCascade,
      });
      showNotification({ type: "success", title: "Revoked", message: cap.id });
      reset();
      onRevoked();
      onClose();
    } catch (err) {
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Revoke failed";
      showNotification({ type: "error", title: "Revoke failed", message: msg });
    }
  };

  return (
    <AlertDialog open={!!cap} onOpenChange={(o) => !o && handleClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Revoke this capability?</AlertDialogTitle>
          <AlertDialogDescription>
            <span className="font-mono text-foreground">{cap?.id}</span> will
            stop verifying immediately on every plane. Revocation is checked
            locally by interceptors using a denylist that purges after the
            natural expiry, so the cost of revoking is bounded.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5">
            <Label className="text-xs" htmlFor="revoke-reason">
              Reason (optional)
            </Label>
            <Input
              id="revoke-reason"
              placeholder="leaked / superseded / agent retired"
              value={revokeReason}
              onChange={(e) => setRevokeReason(e.target.value)}
            />
          </div>
          <label className="flex cursor-pointer items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={revokeCascade}
              onChange={(e) => setRevokeCascade(e.target.checked)}
              className="size-4 accent-primary"
            />
            <span>
              Cascade to children — revoke every delegation under this one.
            </span>
          </label>
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={handleRevoke}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
          >
            Revoke
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
