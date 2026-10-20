"use client";

import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { useNotification } from "@/components/ui/Notification";
import { apiTokenClient } from "@/lib/connect/client";
import type { APIToken } from "@/gen/paladin/admin/v1/api_token_service_pb";
import { errorMessage } from "@/hooks/errorContract";

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
      const msg = errorMessage(err, "Revoke failed");
      showNotification({
        type: "error",
        title: "Revoke failed",
        message: msg,
      });
    }
  };

  return (
    <ConfirmModal
      isOpen={!!token}
      onClose={onClose}
      onConfirm={handleRevoke}
      type="danger"
      title="Revoke this token?"
      message={
        <p>
          <span className="font-mono text-foreground">
            paladin_pat_{token?.prefix}…
          </span>{" "}
          stops accepting traffic immediately.
        </p>
      }
      warning="A revoke cannot be undone. Whatever uses this token needs a new one to keep working."
      confirmText="Revoke"
      // A revoke cannot be undone, and whatever runs on the token stops at
      // once. Typed as the verb: a display name is optional and the prefix
      // is opaque.
      typeToConfirm="revoke"
    />
  );
}
