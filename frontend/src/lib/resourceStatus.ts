// Where a tenant, a bucket and a storage backend stand, as the status panels
// at the top of their pages say it: each fact a tone from lib/ui/status, a
// word or two, and one line of what it means. Pure, so every panel and its
// tests read the resource the same way.

import type { PolicyLayer } from "@/gen/paladin/admin/v1/policy_service_pb";
import type { TenantBudget } from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import type {
  Bucket,
  Quota,
  StorageBackend,
} from "@/gen/paladin/admin/v1/types_pb";
import { capPercent } from "@/app/tenants/[id]/quotas/_caps";
import { PROVISION_STATE } from "@/lib/bucketProvision";
import { budgetLimit, budgetUnit } from "@/lib/format/budgetCeiling";
import {
  formatMoney,
  moneyPercent,
  moneyToNanos,
  unitOf,
} from "@/lib/format/money";
import { lifecycleState } from "@/app/storage-backends/backendView";
import { StatusTone } from "@/lib/ui/status";

/** A fact as a status card shows it. */
export interface StatusFact {
  tone: StatusTone;
  value: string;
  detail: string;
}

// ─── Tenant ─────────────────────────────────────────────────────────────────

/** A tenant in the trash is frozen: paused on purpose, not broken. */
export function tenantHealth(trashed: boolean): StatusFact {
  return trashed
    ? {
        tone: StatusTone.Suspended,
        value: "Frozen",
        detail: "In the trash: it takes no changes until it is restored.",
      }
    : {
        tone: StatusTone.Success,
        value: "Active",
        detail: "It takes changes, and its credentials work.",
      };
}

/** Past this share of a cap, a quota is near its limit (as the quotas tab colours it). */
export const QUOTA_NEAR_PCT = 70;
const FULL_PCT = 100;

const QUOTA_CAPS = [
  { name: "Storage", cap: "maxTotalBytes", used: "totalBytes" },
  { name: "Objects", cap: "maxObjectCount", used: "objectCount" },
  { name: "Bytes today", cap: "maxBytesPerDay", used: "bytesToday" },
  { name: "Objects today", cap: "maxObjectsPerDay", used: "objectsToday" },
] as const;

/**
 * The quota by its fullest cap: one cap at its limit refuses uploads however
 * much room the others have. A cap that is absent is none; a quota with none
 * set, or no quota at all, limits nothing.
 */
export function quotaStanding(quota: Quota | null): StatusFact {
  const usage = quota?.usage;
  const shares = QUOTA_CAPS.flatMap(({ name, cap, used }) => {
    const limit = quota?.[cap];
    if (limit === undefined) return [];
    return [{ name, pct: capPercent(usage?.[used] ?? 0n, limit) }];
  });
  if (shares.length === 0) {
    return {
      tone: StatusTone.Success,
      value: "Unlimited",
      detail: "No caps are set.",
    };
  }
  const worst = shares.reduce((w, s) => (s.pct > w.pct ? s : w));
  const share = `${worst.name} at ${Math.round(worst.pct)}% of its cap.`;
  if (worst.pct >= FULL_PCT) {
    return {
      tone: StatusTone.Danger,
      value: "At limit",
      detail: `${worst.name} is at its cap: uploads are refused.`,
    };
  }
  if (worst.pct >= QUOTA_NEAR_PCT) {
    return { tone: StatusTone.Warning, value: "Near limit", detail: share };
  }
  return { tone: StatusTone.Success, value: "Within limits", detail: share };
}

/** Past this share of its cap, a budget is near it (as the budget tile colours it). */
export const BUDGET_NEAR_PCT = 80;

/** A tenant's budget read once for every view of it: the tile and the panel. */
export interface BudgetReading extends StatusFact {
  /** The share of the cap spent, 0–100; null without a cap. */
  pct: number | null;
  overCap: boolean;
  capped: boolean;
}

/**
 * Where a tenant's spend stands against its budget. With no budget there is
 * nothing to stand against; with no cap the spend is shown as it is.
 */
export function budgetStanding(budget: TenantBudget | null): BudgetReading {
  if (!budget) {
    return {
      tone: StatusTone.Unknown,
      value: "No budget",
      detail: "No budget is set, so spend is not tracked against one.",
      pct: null,
      overCap: false,
      capped: false,
    };
  }
  const cap = budgetLimit(budget.budgetCeiling);
  const spent = budget.spent;
  // XXX (no currency) when neither carries a unit: a tenant metering in units
  // is not shown as spending dollars.
  const unit = budgetUnit(budget.budgetCeiling) || unitOf(spent);
  const shown = formatMoney(spent, { fallbackUnit: unit });
  if (cap === undefined) {
    return {
      tone: StatusTone.Success,
      value: "Unlimited",
      detail: `${shown} spent, with no cap.`,
      pct: null,
      overCap: false,
      capped: false,
    };
  }
  // A cap of zero is full from the start.
  const pct = Math.min(FULL_PCT, moneyPercent(spent, cap) ?? FULL_PCT);
  const overCap = moneyToNanos(spent) > moneyToNanos(cap);
  const detail = `${shown} of ${formatMoney(cap, { fallbackUnit: unit })} spent.`;
  const base = { detail, pct, overCap, capped: true };
  if (overCap) return { ...base, tone: StatusTone.Danger, value: "Over cap" };
  if (pct > BUDGET_NEAR_PCT) {
    return { ...base, tone: StatusTone.Warning, value: "Near cap" };
  }
  return { ...base, tone: StatusTone.Success, value: "Within cap" };
}

/** The platform's own layer, as GetEffectivePolicy names its source. */
const BUILT_IN_LAYER = "built-in";

/**
 * The layers that decide a resource's requests. One that does not compile is
 * frozen: its whole scope is denied but for its repair, which is a fault.
 */
export function policyStanding(layers: readonly PolicyLayer[]): StatusFact {
  const frozen = layers.filter((l) => l.frozen);
  if (frozen.length > 0) {
    return {
      tone: StatusTone.Danger,
      value: frozen.length === 1 ? "Layer frozen" : `${frozen.length} frozen`,
      detail: `${frozen.map((l) => l.source).join(", ")} does not compile: everything there is denied but its repair.`,
    };
  }
  const own = layers.filter((l) => l.source !== BUILT_IN_LAYER).length;
  return {
    tone: StatusTone.Success,
    value: `${layers.length} ${layers.length === 1 ? "layer" : "layers"}`,
    detail:
      own === 0
        ? "Only the built-in layer applies."
        : `The built-in layer and ${own} of this tenant's.`,
  };
}

// ─── Bucket ─────────────────────────────────────────────────────────────────

/** Where the bucket is in being created on, or removed from, its backend. */
export function provisionStatus(state: string): StatusFact {
  switch (state || PROVISION_STATE.ready) {
    case PROVISION_STATE.ready:
      return {
        tone: StatusTone.Success,
        value: "Ready",
        detail: "It exists on its backend and takes collections and uploads.",
      };
    case PROVISION_STATE.pending:
      return {
        tone: StatusTone.Progress,
        value: "Provisioning",
        detail: "Being created on its backend; nothing binds to it yet.",
      };
    case PROVISION_STATE.deleting:
      return {
        tone: StatusTone.Progress,
        value: "Deleting",
        detail: "Being removed from its backend.",
      };
    case PROVISION_STATE.failed:
      return {
        tone: StatusTone.Danger,
        value: "Failed",
        detail: "Creating it on its backend failed and will not be retried.",
      };
    case PROVISION_STATE.deletionFailed:
      return {
        tone: StatusTone.Danger,
        value: "Delete failed",
        detail: "Removing it from its backend failed and will not be retried.",
      };
    default:
      return {
        tone: StatusTone.Unknown,
        value: state,
        detail: "A state this console does not know.",
      };
  }
}

/**
 * What keeps the bucket's objects from being lost to a delete or an
 * overwrite. Neither on is a choice, not a fault, so it reads as off.
 */
export function protectionStatus(
  bucket: Pick<Bucket, "versioning" | "objectLock">,
): StatusFact {
  const versioned = bucket.versioning?.enabled ?? false;
  const locked = bucket.objectLock?.enabled ?? false;
  if (versioned && locked) {
    return {
      tone: StatusTone.Success,
      value: "Versioned, locked",
      detail: "Old versions are kept, and retention holds objects.",
    };
  }
  if (versioned) {
    return {
      tone: StatusTone.Success,
      value: "Versioned",
      detail: "Old versions are kept; there is no object lock.",
    };
  }
  if (locked) {
    return {
      tone: StatusTone.Success,
      value: "Locked",
      detail: "Retention holds objects; old versions are not kept.",
    };
  }
  return {
    tone: StatusTone.Suspended,
    value: "Off",
    detail: "Neither versioning nor object lock is on.",
  };
}

/** Whether the bucket copies its objects to another, and where. */
export function replicationStatus(
  bucket: Pick<Bucket, "replication">,
): StatusFact {
  const r = bucket.replication;
  if (!r?.enabled) {
    return {
      tone: StatusTone.Suspended,
      value: "Off",
      detail: "Objects are not copied to another bucket.",
    };
  }
  if (!r.destinationBucket) {
    return {
      tone: StatusTone.Warning,
      value: "No destination",
      detail: "Replication is on but names no bucket to copy to.",
    };
  }
  return {
    tone: StatusTone.Success,
    value: "On",
    detail: `To ${r.destinationBucket}${r.filter ? ", filtered" : ""}.`,
  };
}

/** Whether the bucket adds a layer of its own to the policy. */
export function bucketPolicyStatus(
  bucket: Pick<Bucket, "cedarPolicy">,
): StatusFact {
  return bucket.cedarPolicy.trim()
    ? {
        tone: StatusTone.Success,
        value: "Overlay set",
        detail: "A bucket layer applies on top of each tenant's.",
      }
    : {
        tone: StatusTone.Suspended,
        value: "No overlay",
        detail: "Only the built-in and tenant layers apply.",
      };
}

// ─── Storage backend ────────────────────────────────────────────────────────

/** What the backend's lifecycle state lets through. */
export function backendLifecycleStatus(
  backend: Pick<StorageBackend, "enabled" | "readOnly">,
): StatusFact {
  const { label, tone } = lifecycleState(backend);
  const detail = !backend.enabled
    ? "It refuses every request until it is enabled."
    : backend.readOnly
      ? "It serves reads and refuses new writes."
      : "It serves reads and writes.";
  return { tone, value: label, detail };
}

/** The advisory maintenance flag: a pause an operator chose, not a gate. */
export function maintenanceStatus(maintenance: boolean): StatusFact {
  return maintenance
    ? {
        tone: StatusTone.Suspended,
        value: "Maintenance",
        detail: "Marked as under maintenance; requests are still served.",
      }
    : {
        tone: StatusTone.Success,
        value: "In service",
        detail: "Not marked as under maintenance.",
      };
}
