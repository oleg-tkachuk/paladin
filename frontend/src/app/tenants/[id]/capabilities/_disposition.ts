import {
  CapabilityDisposition,
  type Capability,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import type { ComponentProps } from "react";
import type { Badge } from "@/components/ui/badge";
import { isExpired } from "./_constants";

// A capability's status: its disposition as CapabilityService.List names it,
// the same rules the platform's capability counts use — revoked, itself or
// through a revoked ancestor, over expired over active.

/** The dispositions the list filters by, in the order the filter shows them. */
export const STATUS_FILTERS = [
  { disposition: CapabilityDisposition.ACTIVE, label: "active" },
  { disposition: CapabilityDisposition.EXPIRED, label: "expired" },
  { disposition: CapabilityDisposition.REVOKED, label: "revoked" },
] as const;

/** What the list shows first: the capabilities still in force. */
export const DEFAULT_STATUSES: readonly CapabilityDisposition[] = [
  CapabilityDisposition.ACTIVE,
];

/**
 * statuses with d turned on or off, in the filter's order. The last one is
 * kept: a list of no status would ask the server for its default instead.
 */
export function toggleStatus(
  statuses: readonly CapabilityDisposition[],
  d: CapabilityDisposition,
  on: boolean,
): CapabilityDisposition[] {
  const next = STATUS_FILTERS.map((f) => f.disposition).filter((s) =>
    s === d ? on : statuses.includes(s),
  );
  return next.length > 0 ? next : [...statuses];
}

/** A status badge: its words and its colour. */
type StatusBadge = {
  label: string;
  variant: NonNullable<ComponentProps<typeof Badge>["variant"]>;
};

const STATUS_BADGES: Record<
  | CapabilityDisposition.ACTIVE
  | CapabilityDisposition.EXPIRED
  | CapabilityDisposition.REVOKED,
  StatusBadge
> = {
  [CapabilityDisposition.ACTIVE]: { label: "active", variant: "success" },
  [CapabilityDisposition.EXPIRED]: { label: "expired", variant: "outline" },
  [CapabilityDisposition.REVOKED]: { label: "revoked", variant: "destructive" },
};

/**
 * The badge a capability's status reads as. A capability read by Get carries
 * no disposition; its expiry is all it says of one.
 */
export function capabilityStatus(c: Capability): StatusBadge {
  switch (c.disposition) {
    case CapabilityDisposition.ACTIVE:
    case CapabilityDisposition.EXPIRED:
    case CapabilityDisposition.REVOKED:
      return STATUS_BADGES[c.disposition];
    default:
      return STATUS_BADGES[
        isExpired(c)
          ? CapabilityDisposition.EXPIRED
          : CapabilityDisposition.ACTIVE
      ];
  }
}
