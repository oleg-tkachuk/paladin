"use client";

import { useState } from "react";

import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  useNotification,
  type Notification,
} from "@/components/ui/Notification";
import type { CapabilityServiceRevokeBiscuitResponse } from "@/gen/paladin/admin/v1/capability_service_pb";
import { formatTimestamp } from "@/lib/format/timestamp";
import { capabilityClient } from "@/lib/connect/client";
import { errorMessage } from "@/hooks/errorContract";
import { isJWT } from "./_biscuit";

/**
 * What a copy's revoke did, as the notification says it. A revoke is
 * idempotent: a second one keeps the first entry, and says so rather than
 * claim it revoked anything.
 */
export function noticeOfBiscuitRevoke(
  res: Pick<
    CapabilityServiceRevokeBiscuitResponse,
    "capabilityId" | "alreadyRevoked" | "revocation"
  >,
): Omit<Notification, "id"> {
  if (res.alreadyRevoked) {
    return {
      type: "info",
      title: "Already revoked",
      message: `This copy of capability ${res.capabilityId} was revoked on ${formatTimestamp(res.revocation?.revokedAt)}; that revocation stands.`,
    };
  }
  return {
    type: "success",
    title: "Copy revoked",
    message: `A copy of capability ${res.capabilityId}`,
  };
}

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
      showNotification(noticeOfBiscuitRevoke(res));
      handleClose();
    } catch (err) {
      const msg = errorMessage(err, "Revoke failed");
      showNotification({ type: "error", title: "Revoke failed", message: msg });
    }
  };

  return (
    <ConfirmModal
      isOpen={open}
      onClose={handleClose}
      onConfirm={handleRevoke}
      type="danger"
      title="Revoke one copy of a Biscuit"
      message={
        <p>
          Paste the Biscuit an agent holds. It stops verifying, and so does
          every copy narrowed from it.
        </p>
      }
      warning="The capability itself, the copy this one was narrowed from and its other copies keep working — revoke the capability to stop them all. A revoke cannot be undone."
      confirmText="Revoke copy"
      confirmDisabled={!trimmed || jwt}
    >
      <div className="space-y-3 text-left">
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
    </ConfirmModal>
  );
}
