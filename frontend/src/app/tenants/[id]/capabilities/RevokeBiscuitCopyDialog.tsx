"use client";

import { useState } from "react";

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
import { Textarea } from "@/components/ui/textarea";
import { useNotification } from "@/components/ui/Notification";
import { capabilityClient } from "@/lib/connect/client";
import { errorMessage } from "@/hooks/errorContract";
import { isJWT } from "./_biscuit";

/**
 * Revokes one copy of a capability's Biscuit. Copies are narrowed offline by
 * whoever holds them, so the server keeps no list of them: the operator
 * pastes the copy itself, and the server revokes it and every copy narrowed
 * from it. The capability, and copies the pasted one was not narrowed from,
 * keep working.
 */
export function RevokeBiscuitCopyDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { showNotification } = useNotification();
  const [token, setToken] = useState("");
  const [reason, setReason] = useState("");

  const trimmed = token.trim();
  const jwt = isJWT(trimmed);

  const handleClose = () => {
    setToken("");
    setReason("");
    onClose();
  };

  const handleRevoke = async () => {
    if (!trimmed || jwt) return;
    try {
      const res = await capabilityClient.revokeBiscuit({
        token: trimmed,
        reason: reason.trim(),
      });
      showNotification({
        type: "success",
        title: "Copy revoked",
        message: `A copy of capability ${res.capabilityId}`,
      });
      handleClose();
    } catch (err) {
      const msg = errorMessage(err, "Revoke failed");
      showNotification({ type: "error", title: "Revoke failed", message: msg });
    }
  };

  return (
    <AlertDialog open={open} onOpenChange={(o) => !o && handleClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Revoke one copy of a Biscuit</AlertDialogTitle>
          <AlertDialogDescription>
            Paste the Biscuit an agent holds. It stops verifying, and so does
            every copy narrowed from it. The capability itself, the copy this
            one was narrowed from and its other copies keep working — revoke the
            capability to stop them all.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5">
            <Label className="text-xs" htmlFor="revoke-copy-token">
              Biscuit
            </Label>
            <Textarea
              id="revoke-copy-token"
              rows={4}
              spellCheck={false}
              autoComplete="off"
              className="font-mono text-xs"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              aria-invalid={jwt}
              aria-describedby={jwt ? "revoke-copy-jwt" : undefined}
            />
            {jwt && (
              <p id="revoke-copy-jwt" className="text-xs text-destructive">
                This is a JWT, not a Biscuit. A JWT has no copies: revoke its
                capability instead.
              </p>
            )}
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs" htmlFor="revoke-copy-reason">
              Reason (optional)
            </Label>
            <Input
              id="revoke-copy-reason"
              placeholder="leaked / sub-agent retired"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </div>
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={handleRevoke}
            disabled={!trimmed || jwt}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
          >
            Revoke copy
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
