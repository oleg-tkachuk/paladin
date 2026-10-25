import React from "react";
import Link from "next/link";

import type {
  Capability,
  CapabilityServiceGetResponse,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { CapabilityUsage } from "@/lib/queries/details";
import { ABSTRACT_UNIT_CODE, formatMoney } from "@/lib/format/money";
import { copyLimit, requestLimit } from "./_ceilings";
import { budgetLimit, budgetUnit } from "@/lib/format/budgetCeiling";
import { SPEND_DIGITS, principalKindLabel } from "./_constants";
import { capabilityStatus } from "./_disposition";
import { Timestamp } from "@/components/Timestamp";
import { CopyableId } from "@/components/ui/CopyableId";
import { ActorName } from "@/components/features/audit/ActorName";
import { Duration } from "@/components/Duration";
import { CapabilityOpGrants } from "@/components/features/capabilities/CapabilityOpGrants";

// UsageEntry is the capability's usage: undefined while loading,
// "unavailable" when the read failed.
export type UsageEntry = CapabilityUsage | "unavailable" | undefined;

// RecordEntry is the capability's record as read by CapabilityService.Get:
// undefined while loading, "unavailable" when the read failed.
export type RecordEntry =
  CapabilityServiceGetResponse | "unavailable" | undefined;

// How much of an ancestor's id names it in a row; the whole is its tooltip.
const ANCESTOR_ID_SHOWN = 8;

// recordText is what a field read from the record shows while there is none.
function recordText(record: RecordEntry): string | undefined {
  if (record === undefined) return "loading…";
  if (record === "unavailable") return "unavailable";
  return undefined;
}

// DetailsBody renders every field of a Capability in a stacked label/value
// layout, what it is now first. Its id is the panel's header, with a copy;
// long free text (prefixes, URIs) `break-all` so the panel does not widen.
export function DetailsBody({
  cap,
  usageEntry,
  record,
}: {
  cap: Capability;
  usageEntry: UsageEntry;
  record: RecordEntry;
}) {
  // Capability without a budget unit (metering-only) renders as XXX, not
  // USD — the bare "$" would imply currency where none is pinned.
  const unit = budgetUnit(cap.caveats?.budgetCeiling) ?? ABSTRACT_UNIT_CODE;
  const maxRequests = requestLimit(cap.caveats);
  const maxCopies = copyLimit(cap.caveats);
  const maxBudget = budgetLimit(cap.caveats?.budgetCeiling);
  const status = capabilityStatus(
    cap,
    typeof record === "object" && record.revocation !== undefined,
  );

  return (
    // One column, what the capability is now first: two columns left each
    // value too narrow for an id or a timestamp, which then broke mid-way.
    <div className="py-1">
      <DetailsSection title="Lifetime">
        <DetailRow
          label="Status"
          value={
            <Badge variant={status.variant} className={T.code}>
              {status.label}
            </Badge>
          }
        />
        <DetailRow
          label="Issued"
          value={<Timestamp ts={cap.issuedAt} />}
          mono
        />
        <DetailRow
          label="Not before"
          value={<Timestamp ts={cap.notBefore} />}
          mono
        />
        <DetailRow
          label="Expires"
          value={<Timestamp ts={cap.expiresAt} />}
          mono
        />
        <DetailRow
          label="Valid for"
          value={
            <Duration from={cap.notBefore ?? cap.issuedAt} to={cap.expiresAt} />
          }
          mono
        />
      </DetailsSection>
      <DetailsSection title="Principal">
        <DetailRow label="Kind" value={principalKindLabel(cap.subject?.kind)} />
        <DetailRow
          label="Subject"
          value={cap.subject?.subject || "—"}
          mono
          breakAll
        />
      </DetailsSection>

      <DetailsSection title="Authorization">
        <DetailRow
          label="Audience"
          value={
            cap.audience.length > 0 ? (
              <div className="flex flex-wrap gap-1">
                {cap.audience.map((a) => (
                  <Badge key={a} variant="secondary" className={T.code}>
                    {a}
                  </Badge>
                ))}
              </div>
            ) : (
              "—"
            )
          }
        />
        <DetailRow
          label="Allowed ops"
          value={
            (cap.caveats?.ops ?? []).length > 0 ? (
              <div className="flex flex-wrap gap-1">
                {cap.caveats!.ops.map((op) => (
                  <Badge key={op} variant="outline" className={T.code}>
                    {op}
                  </Badge>
                ))}
              </div>
            ) : (
              "—"
            )
          }
        />
        {(cap.caveats?.ops ?? []).length > 0 && (
          <DetailRow
            label="Unlocks"
            value={<CapabilityOpGrants ops={cap.caveats!.ops} />}
          />
        )}
      </DetailsSection>

      <DetailsSection title="Caveats">
        <DetailRow
          label="Resource prefixes"
          value={
            (cap.caveats?.resourcePrefixes ?? []).length > 0 ? (
              <ul className="space-y-0.5 font-mono text-xs">
                {cap.caveats!.resourcePrefixes.map((p) => (
                  <li key={p} className="break-all">
                    {p}
                  </li>
                ))}
              </ul>
            ) : (
              "no restriction"
            )
          }
        />
        <DetailRow
          label="Resource URIs"
          value={
            (cap.caveats?.resourceUris ?? []).length > 0 ? (
              <ul className="space-y-0.5 font-mono text-xs">
                {cap.caveats!.resourceUris.map((u) => (
                  <li key={u} className="break-all">
                    {u}
                  </li>
                ))}
              </ul>
            ) : (
              "no restriction"
            )
          }
        />
        <DetailRow
          label="Source IP CIDR"
          value={
            (cap.caveats?.sourceIpCidr ?? []).length > 0 ? (
              <div className="flex flex-wrap gap-1">
                {cap.caveats!.sourceIpCidr.map((c) => (
                  <Badge key={c} variant="outline" className={T.code}>
                    {c}
                  </Badge>
                ))}
              </div>
            ) : (
              "any IP"
            )
          }
        />
        <DetailRow
          label="Allow tainted read"
          value={cap.caveats?.allowTaintedRead ? "yes" : "no"}
        />
        <DetailRow
          label="Idempotency key required"
          value={cap.caveats?.idempotencyKeyRequired ? "yes" : "no"}
        />
      </DetailsSection>

      <DetailsSection title="Limits & usage">
        <DetailRow
          label="Max requests"
          value={maxRequests === null ? "unlimited" : maxRequests.toString()}
          mono
        />
        <DetailRow
          label="Max Biscuit copies"
          value={maxCopies === null ? "unlimited" : maxCopies.toString()}
          mono
        />
        <DetailRow
          label="Requests used"
          value={
            usageEntry === undefined
              ? "loading…"
              : usageEntry === "unavailable"
                ? "unavailable"
                : usageEntry === "never"
                  ? "never used"
                  : usageEntry.requestCount.toString()
          }
          mono
        />
        <DetailRow label="Currency / Unit" value={unit} mono />
        <DetailRow
          label="Max budget"
          value={maxBudget === undefined ? "unlimited" : formatMoney(maxBudget)}
          mono
        />
        <DetailRow
          label="Spent"
          value={
            usageEntry === undefined
              ? "loading…"
              : usageEntry === "unavailable"
                ? "unavailable"
                : usageEntry === "never"
                  ? formatMoney(undefined, {
                      fallbackUnit: unit,
                      fractionDigits: SPEND_DIGITS,
                    })
                  : formatMoney(usageEntry.spent, {
                      fallbackUnit: unit,
                      fractionDigits: SPEND_DIGITS,
                    })
          }
          mono
        />
      </DetailsSection>

      <DetailsSection title="Issuance & revocation">
        <IssuanceRows record={record} tenantId={cap.subject?.tenantId ?? ""} />
      </DetailsSection>

      <DetailsSection title="Identifiers">
        <DetailRow label="Issuer" value={cap.issuer || "—"} mono />
        <DetailRow
          label="Parent"
          value={
            cap.parentId ? (
              <CopyableId value={cap.parentId} label="parent capability ID" />
            ) : (
              "—"
            )
          }
        />
        <DetailRow
          label="Tenant"
          value={
            cap.subject?.tenantId ? (
              <CopyableId value={cap.subject.tenantId} label="tenant ID" />
            ) : (
              "—"
            )
          }
        />
      </DetailsSection>
    </div>
  );
}

// IssuanceRows shows who asked for the capability and its own revocation
// entry. A capability stopped only through an ancestor has none of its own.
function IssuanceRows({
  record,
  tenantId,
}: {
  record: RecordEntry;
  /** The capability's tenant: where an actor without one of its own is. */
  tenantId: string;
}) {
  const pending = recordText(record);
  if (pending !== undefined || typeof record !== "object") {
    return (
      <>
        <DetailRow label="Issued by" value={pending} />
        <DetailRow label="Revoked" value={pending} />
      </>
    );
  }
  const issuer = record.issuedBy;
  const rev = record.revocation;
  const stopped = record.stoppedBy;
  return (
    <>
      <DetailRow
        label="Issued by"
        value={
          issuer?.subject ? (
            <ActorName
              subject={issuer.subject}
              tenantId={issuer.tenantId || tenantId}
            />
          ) : (
            "—"
          )
        }
        breakAll
      />
      {rev ? (
        <>
          <DetailRow
            label="Revoked"
            value={<Timestamp ts={rev.revokedAt} />}
            mono
          />
          <DetailRow
            label="Revoked by"
            value={
              rev.actor ? (
                <ActorName subject={rev.actor} tenantId={tenantId} />
              ) : (
                "—"
              )
            }
            breakAll
          />
          <DetailRow label="Reason" value={rev.reason || "—"} />
          <DetailRow
            label="Cascade"
            value={
              rev.cascade ? "yes — with everything delegated from it" : "no"
            }
          />
        </>
      ) : stopped?.revocation ? (
        // No entry of its own: an ancestor's revocation stopped it, which
        // the record names so the operator can open it.
        <>
          <DetailRow
            label="Revoked"
            value={
              <span>
                through its ancestor{" "}
                <Link
                  href={`?id=${encodeURIComponent(stopped.capabilityId)}`}
                  className="font-mono text-xs text-primary hover:underline"
                  title={stopped.capabilityId}
                >
                  {stopped.capabilityId.slice(0, ANCESTOR_ID_SHOWN)}…
                </Link>
              </span>
            }
          />
          <DetailRow
            label="On"
            value={<Timestamp ts={stopped.revocation.revokedAt} />}
            mono
          />
          <DetailRow label="Reason" value={stopped.revocation.reason || "—"} />
        </>
      ) : (
        <DetailRow label="Revoked" value="not revoked itself" />
      )}
    </>
  );
}

function DetailsSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    // `break-inside-avoid` keeps the section as one block under the parent's
    // CSS multi-column layout; `mb-4` gives the inter-section breathing room.
    <div className="mb-4 break-inside-avoid space-y-2 last:mb-0">
      <h3 className={T.label}>{title}</h3>
      <dl className="grid grid-cols-[9rem_1fr] gap-x-4 gap-y-1.5 text-sm">
        {children}
      </dl>
    </div>
  );
}

function DetailRow({
  label,
  value,
  mono,
  breakAll,
}: {
  label: string;
  value: React.ReactNode;
  mono?: boolean;
  breakAll?: boolean;
}) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd
        className={cn(
          mono && "font-mono text-xs",
          breakAll && "break-all",
          "min-w-0",
        )}
      >
        {value}
      </dd>
    </>
  );
}
