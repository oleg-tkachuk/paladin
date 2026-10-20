import { StatusTone } from "@/lib/ui/status";

// Bucket.provision_state, as admindomain.BucketProvisionState* spells it.
export const PROVISION_STATE = {
  pending: "pending",
  ready: "ready",
  failed: "failed",
  deleting: "deleting",
  deletionFailed: "deletion_failed",
} as const;

/** How often a list holding an in-flight bucket asks again. */
export const PROVISION_POLL_MS = 3_000;

/** A state the reconciler has yet to move on from. */
export function isProvisionInFlight(state: string): boolean {
  return (
    state === PROVISION_STATE.pending || state === PROVISION_STATE.deleting
  );
}

/**
 * A provisioning state as a tone and the words for it. An empty state is a
 * bucket the reconciler never had to settle: ready, as ProvisionStateBadge
 * reads it too.
 */
export function provisionStatus(state: string): {
  label: string;
  tone: StatusTone;
} {
  switch (state || PROVISION_STATE.ready) {
    case PROVISION_STATE.ready:
      return { label: "Ready", tone: StatusTone.Success };
    case PROVISION_STATE.pending:
      return { label: "Provisioning", tone: StatusTone.Progress };
    case PROVISION_STATE.deleting:
      return { label: "Deleting", tone: StatusTone.Progress };
    case PROVISION_STATE.failed:
      return { label: "Provisioning failed", tone: StatusTone.Danger };
    case PROVISION_STATE.deletionFailed:
      return { label: "Deletion failed", tone: StatusTone.Danger };
    default:
      return { label: state, tone: StatusTone.Unknown };
  }
}
