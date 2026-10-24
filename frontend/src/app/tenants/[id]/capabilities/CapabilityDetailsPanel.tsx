"use client";

import { TrashIcon } from "@heroicons/react/24/outline";
import { useQuery } from "@tanstack/react-query";

import { DetailPanel } from "@/components/ui/DetailPanel";
import { Button } from "@/components/ui/button";
import {
  type CapabilityUsage,
  capabilityDetailQuery,
  capabilityUsageQuery,
} from "@/lib/queries/details";
import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import { T } from "@/lib/ui/typography";

import { DetailsBody, type RecordEntry, type UsageEntry } from "./_details";
import { isCapabilityRevoked } from "./_disposition";

/**
 * A capability's details, in a panel beside the list. Renders the row data
 * and the usage entry the page already fetched, and reads the record
 * (CapabilityService.Get) for who issued the capability and why it was
 * revoked, which the list does not carry. A capability the list does not
 * hold — revoked, expired, or linked by id — has no usage from the page, so
 * the panel reads its own.
 */
export function CapabilityDetailsPanel({
  id,
  cap,
  usageEntry,
  onClose,
  onRevoke,
  onOpen,
}: {
  /** The capability to show; null keeps the panel closed. */
  id: string | null;
  /** Its row from the page's list, shown before the record answers. */
  cap: Capability | null;
  /** The page's usage for it; undefined when its list did not read one. */
  usageEntry: CapabilityUsage | undefined;
  onClose: () => void;
  /** Asks to revoke the capability shown; the page confirms it. */
  onRevoke: (cap: Capability) => void;
  /** Moves the panel to another capability. */
  onOpen: (id: string) => void;
}) {
  const recordQuery = useQuery({
    ...capabilityDetailQuery(id ?? ""),
    enabled: !!id,
  });
  const usageQuery = useQuery({
    ...capabilityUsageQuery(id ?? ""),
    enabled: !!id && usageEntry === undefined,
  });
  const usage: UsageEntry =
    usageEntry ?? (usageQuery.isError ? "unavailable" : usageQuery.data);
  // A capability linked by id may not be in the list the page browsed.
  const shown = cap ?? recordQuery.data?.capability ?? null;
  const record: RecordEntry = recordQuery.isError
    ? "unavailable"
    : recordQuery.data;

  return (
    <DetailPanel
      open={!!id}
      onClose={onClose}
      rowId={id}
      title="Capability details"
      identifier={id ?? undefined}
      footer={
        // A revoked capability has nothing left to revoke.
        shown &&
        !isCapabilityRevoked(
          shown,
          recordQuery.data?.revocation !== undefined,
        ) ? (
          <Button
            variant="destructive"
            size="sm"
            onClick={() => onRevoke(shown)}
          >
            <TrashIcon className="size-4" />
            Revoke
          </Button>
        ) : null
      }
    >
      {shown ? (
        <DetailsBody
          cap={shown}
          usageEntry={usage}
          record={record}
          onOpen={onOpen}
        />
      ) : (
        <p className={T.hint}>
          {recordQuery.isError
            ? `No capability ${id} is on record, or it is not visible to you.`
            : "Reading the capability…"}
        </p>
      )}
    </DetailPanel>
  );
}
