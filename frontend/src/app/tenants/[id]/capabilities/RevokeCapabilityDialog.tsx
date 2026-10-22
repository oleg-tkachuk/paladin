"use client";

import { useState } from "react";

import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { formatTimestamp } from "@/lib/format/timestamp";
import {
  useNotification,
  type Notification,
} from "@/components/ui/Notification";
import { capabilityClient } from "@/lib/connect/client";
import {
  CapabilityDisposition,
  type Capability,
  type CapabilityServiceRevokeResponse,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import { errorMessage } from "@/hooks/errorContract";

/**
 * What a revoke did, as the notification says it. A revoke is idempotent: a
 * second one keeps the first entry, and says so rather than claim it revoked
 * anything. One of a capability already stopped — through an ancestor, or by
 * expiring — writes its entry for the audit but stops nothing new, which the
 * server reports as the disposition the capability had before the call.
 */
export function noticeOfRevoke(
  id: string,
  res: Pick<
    CapabilityServiceRevokeResponse,
    "alreadyRevoked" | "revocation" | "before"
  >,
): Omit<Notification, "id"> {
  if (res.alreadyRevoked) {
    return {
      type: "info",
      title: "Already revoked",
      message: `${id} was revoked on ${formatTimestamp(res.revocation?.revokedAt)}; that revocation stands.`,
    };
  }
  switch (res.before) {
    case CapabilityDisposition.REVOKED:
      return {
        type: "info",
        title: "Revocation recorded",
        message: `${id} had already stopped through a revoked ancestor; the audit trail now names it too.`,
      };
    case CapabilityDisposition.EXPIRED:
      return {
        type: "info",
        title: "Revocation recorded",
        message: `${id} had already expired; the audit trail now records it as revoked.`,
      };
  }
  return { type: "success", title: "Revoked", message: id };
}

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
      const res = await capabilityClient.revoke({
        id: cap.id,
        reason: revokeReason.trim(),
        cascadeChildren: revokeCascade,
      });
      showNotification(noticeOfRevoke(cap.id, res));
      reset();
      onRevoked();
      onClose();
    } catch (err) {
      const msg = errorMessage(err, "Revoke failed");
      showNotification({ type: "error", title: "Revoke failed", message: msg });
    }
  };

  // Every delegation under a revoked capability stops verifying whatever the
  // checkbox says: the verifier checks the whole chain of ancestors (limes
  // Store.Revoke). Cascading only writes a revocation entry for each of them,
  // so the audit trail names everything that stopped. The checkbox used to
  // read as though leaving it off kept the delegations working.
  return (
    <ConfirmModal
      isOpen={!!cap}
      onClose={handleClose}
      onConfirm={handleRevoke}
      type="danger"
      title="Revoke this capability?"
      message={
        <p>
          <span className="font-mono text-foreground">{cap?.id}</span> stops
          verifying immediately on every plane.
        </p>
      }
      warning="Every delegation issued under it stops too. A revoke cannot be undone: holders need a new capability."
      confirmText="Revoke"
      // A revoke cannot be undone and reaches every delegation under the
      // capability, so it is typed out; as the verb, since its id is opaque.
      typeToConfirm="revoke"
    >
      <div className="space-y-3 text-left">
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
        <label className="flex cursor-pointer items-start gap-2 text-sm">
          <input
            type="checkbox"
            checked={revokeCascade}
            onChange={(e) => setRevokeCascade(e.target.checked)}
            className="mt-0.5 size-4 accent-primary"
          />
          <span>
            Record each delegation under it as revoked too, so the audit trail
            names them all.
          </span>
        </label>
      </div>
    </ConfirmModal>
  );
}
