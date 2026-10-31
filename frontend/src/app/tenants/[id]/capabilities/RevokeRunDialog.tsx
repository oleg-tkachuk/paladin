"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  useNotification,
  type Notification,
} from "@/components/ui/Notification";
import { capabilityClient } from "@/lib/connect/client";
import { CAPABILITY_RECORDS_KEY } from "@/lib/queries/details";
import { errorMessage } from "@/hooks/errorContract";

/**
 * What a run's revocation did, as the notification says it. It is
 * idempotent: a repeat revokes nothing and says so rather than claim it
 * stopped anything.
 */
export function noticeOfRevokeRun(
  run: string,
  revoked: bigint,
): Omit<Notification, "id"> {
  if (revoked === 0n) {
    return {
      type: "info",
      title: "Nothing left to revoke",
      message: `Run ${run} has no live capability in this tenant.`,
    };
  }
  return {
    type: "success",
    title: "Run revoked",
    message: `${revoked} ${revoked === 1n ? "capability" : "capabilities"} of run ${run} revoked.`,
  };
}

/**
 * Confirms revoking an agent run: every capability of the run in the
 * tenant, the run's own and through them those delegated from them, with the
 * holds they keep released. The page lists again through onRevoked.
 */
export function RevokeRunDialog({
  tenantId,
  run,
  onClose,
  onRevoked,
}: {
  tenantId: string;
  /** The run to revoke; null keeps the dialog closed. */
  run: string | null;
  onClose: () => void;
  onRevoked: () => void;
}) {
  const { showNotification } = useNotification();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState("");

  const handleClose = () => {
    setReason("");
    onClose();
  };
  const handleRevoke = async () => {
    if (!run) return;
    try {
      const res = await capabilityClient.revokeRun({
        tenantId,
        runId: run,
        reason: reason.trim(),
      });
      showNotification(noticeOfRevokeRun(run, res.revoked));
      void queryClient.invalidateQueries({ queryKey: CAPABILITY_RECORDS_KEY });
      setReason("");
      onRevoked();
      onClose();
    } catch (err) {
      showNotification({
        type: "error",
        title: "Revoke failed",
        message: errorMessage(err, "The run was not revoked"),
      });
    }
  };

  return (
    <ConfirmModal
      isOpen={!!run}
      onClose={handleClose}
      onConfirm={handleRevoke}
      type="danger"
      title="Revoke this agent run?"
      message={
        <p>
          Every capability of run{" "}
          <span className="font-mono text-foreground">{run}</span> in this
          tenant stops verifying immediately on every plane, and the costs it
          holds are released.
        </p>
      }
      warning="Delegations issued under them stop too. A revoke cannot be undone: the run needs new capabilities to go on."
      confirmText="Revoke run"
      // Reaches every capability of the run at once, so it is typed out.
      typeToConfirm="revoke"
    >
      <div className="space-y-1.5 text-left">
        <Label className="text-xs" htmlFor="revoke-run-reason">
          Reason (optional)
        </Label>
        <Input
          id="revoke-run-reason"
          placeholder="runaway agent / task cancelled"
          value={reason}
          onChange={(e) => setReason(e.target.value)}
        />
      </div>
    </ConfirmModal>
  );
}
