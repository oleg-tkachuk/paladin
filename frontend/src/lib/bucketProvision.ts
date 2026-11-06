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

/** A provisioning state, as the server spells it. */
export type ProvisionState =
  (typeof PROVISION_STATE)[keyof typeof PROVISION_STATE];

/** Every provisioning state, in the order a bucket moves through them. */
export const PROVISION_STATES: readonly ProvisionState[] = [
  PROVISION_STATE.ready,
  PROVISION_STATE.pending,
  PROVISION_STATE.deleting,
  PROVISION_STATE.failed,
  PROVISION_STATE.deletionFailed,
];

/** How the console shows a provisioning state, wherever it shows one. */
export interface ProvisionView {
  label: string;
  tone: StatusTone;
  /** One line of what the state means, for a status card. */
  detail: string;
}

/**
 * The one place a provisioning state gets its words and tone: the bucket
 * tables' badge and state filter, the bucket's status card and the topology
 * all read it here.
 */
const PROVISION_VIEW: Record<ProvisionState, ProvisionView> = {
  [PROVISION_STATE.ready]: {
    label: "Ready",
    tone: StatusTone.Success,
    detail: "It exists on its backend and takes collections and uploads.",
  },
  [PROVISION_STATE.pending]: {
    label: "Provisioning",
    tone: StatusTone.Progress,
    detail: "Being created on its backend; nothing binds to it yet.",
  },
  [PROVISION_STATE.deleting]: {
    label: "Deleting",
    tone: StatusTone.Progress,
    detail: "Being removed from its backend.",
  },
  [PROVISION_STATE.failed]: {
    label: "Provisioning failed",
    tone: StatusTone.Danger,
    detail: "Creating it on its backend failed and will not be retried.",
  },
  [PROVISION_STATE.deletionFailed]: {
    label: "Deletion failed",
    tone: StatusTone.Danger,
    detail: "Removing it from its backend failed and will not be retried.",
  },
};

function isProvisionState(state: string): state is ProvisionState {
  return Object.hasOwn(PROVISION_VIEW, state);
}

/**
 * A provisioning state as the console shows it. An empty state is a bucket
 * the reconciler never had to settle, which reads as ready; a state this
 * console does not know is shown as the server spells it.
 */
export function provisionView(state: string): ProvisionView {
  const s = state || PROVISION_STATE.ready;
  if (isProvisionState(s)) return PROVISION_VIEW[s];
  return {
    label: s,
    tone: StatusTone.Unknown,
    detail: "A state this console does not know.",
  };
}

/** A provisioning state as a tone and the words for it. */
export function provisionStatus(state: string): {
  label: string;
  tone: StatusTone;
} {
  const { label, tone } = provisionView(state);
  return { label, tone };
}
