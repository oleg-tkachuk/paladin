"use client";

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
import { useNotification } from "@/components/ui/Notification";
import { apiTokenClient } from "@/lib/connect/client";
import type { APIToken } from "@/gen/paladin/admin/v1/api_token_service_pb";

/**
 * Revoke-confirmation for an M2M token, extracted from the page. Owns the
 * revoke RPC; the page clears its target via onClose and refetches via
 * onRevoked.
 */
export function RevokeTokenDialog({
  token,
  onClose,
  onRevoked,
}: {
  token: APIToken | null;
  onClose: () => void;
  onRevoked: () => void;
}) {
  const { showNotification } = useNotification();

  const handleRevoke = async () => {
    if (!token) return;
    try {
      await apiTokenClient.revoke({ name: token.name });
      showNotification({
        type: "success",
        title: "Token revoked",
        message: token.displayName || token.prefix,
      });
      onClose();
      onRevoked();
    } catch (err) {
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Revoke failed";
      showNotification({
        type: "error",
        title: "Revoke failed",
        message: msg,
      });
    }
  };

  return (
    <AlertDialog open={!!token} onOpenChange={(o) => !o && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Revoke this token?</AlertDialogTitle>
          <AlertDialogDescription>
            The token{" "}
            <span className="font-mono text-foreground">
              paladin_pat_{token?.prefix}…
            </span>{" "}
            will stop accepting traffic immediately. This cannot be undone —
            create a new one to replace.
          </AlertDialogDescription>
        </AlertDialogHeader>
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
