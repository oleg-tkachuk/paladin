import React from "react";

import type {
  Capability,
  CapabilityServiceGetResponse,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { formatMoney, fromMicros } from "@/lib/format/money";
import { PRINCIPAL_KIND_OPTIONS, isExpired } from "./_constants";
import { formatTimestampUTC } from "@/lib/format/timestamp";

type UsageEntry =
  | { requestCount: bigint; spentAmount: number; unitCode: string }
  | "never"
  | undefined;

// RecordEntry is the capability's record as read by CapabilityService.Get:
// undefined while loading, "unavailable" when the read failed.
export type RecordEntry =
  CapabilityServiceGetResponse | "unavailable" | undefined;

// recordText is what a field read from the record shows while there is none.
function recordText(record: RecordEntry): string | undefined {
  if (record === undefined) return "loading…";
  if (record === "unavailable") return "unavailable";
  return undefined;
}

// DetailsBody renders every field of a Capability in a stacked label/value
// layout. Long mono strings (id, parent_id, prefixes) `break-all` so the dialog
// doesn't blow out horizontally on UUIDs. Extracted from page.tsx (props-only).
export function DetailsBody({
  cap,
  usageEntry,
  record,
}: {
  cap: Capability;
  usageEntry: UsageEntry;
  record: RecordEntry;
}) {
  // Capability without an explicit unit_code (legacy or metering-only) renders
  // as UNIT, not USD — the bare "$" would imply currency where none is pinned.
  const unit = cap.caveats?.unitCode || "UNIT";
  const principalKindLabel =
    PRINCIPAL_KIND_OPTIONS.find((o) => Number(o.value) === cap.subject?.kind)
      ?.label ?? `kind:${cap.subject?.kind}`;
  const expired = isExpired(cap);

  return (
    // CSS multi-column layout (columns-2 on md+) with `break-inside-avoid` on
    // each section, so the dialog ends ~half as tall as a single-column stack.
    <div className="md:columns-2 md:gap-x-8 py-1">
      <DetailsSection title="Identity">
        <DetailRow label="Capability ID" value={cap.id} mono breakAll />
        <DetailRow label="Issuer" value={cap.issuer || "—"} mono />
        <DetailRow
          label="Parent ID"
          value={cap.parentId || "—"}
          mono
          breakAll
        />
        <DetailRow label="Generation" value={cap.generation.toString()} mono />
      </DetailsSection>

      <DetailsSection title="Principal">
        <DetailRow label="Kind" value={principalKindLabel} />
        <DetailRow
          label="Subject"
          value={cap.subject?.subject || "—"}
          mono
          breakAll
        />
        <DetailRow
          label="Tenant ID"
          value={cap.subject?.tenantId || "—"}
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
          value={
            (cap.caveats?.maxRequests ?? 0) > 0
              ? cap.caveats!.maxRequests.toString()
              : "unlimited"
          }
          mono
        />
        <DetailRow
          label="Requests used"
          value={
            usageEntry === undefined
              ? "loading…"
              : usageEntry === "never"
                ? "never used"
                : usageEntry.requestCount.toString()
          }
          mono
        />
        <DetailRow label="Currency / Unit" value={unit} mono />
        <DetailRow
          label="Max budget"
          value={
            (cap.caveats?.maxBudgetMicros ?? 0n) > 0n
              ? formatMoney(fromMicros(cap.caveats?.maxBudgetMicros), unit)
              : "unlimited"
          }
          mono
        />
        <DetailRow
          label="Spent"
          value={
            usageEntry === undefined
              ? "loading…"
              : usageEntry === "never"
                ? formatMoney(0, unit, undefined, 4)
                : formatMoney(
                    usageEntry.spentAmount,
                    usageEntry.unitCode || unit,
                    undefined,
                    4,
                  )
          }
          mono
        />
      </DetailsSection>

      <DetailsSection title="Issuance & revocation">
        <IssuanceRows record={record} />
      </DetailsSection>

      <DetailsSection title="Lifetime">
        <DetailRow
          label="Status"
          value={
            <Badge variant={expired ? "outline" : "success"} className={T.code}>
              {expired ? "expired" : "active"}
            </Badge>
          }
        />
        <DetailRow
          label="Issued"
          value={formatTimestampUTC(cap.issuedAt)}
          mono
        />
        <DetailRow
          label="Not before"
          value={formatTimestampUTC(cap.notBefore)}
          mono
        />
        <DetailRow
          label="Expires"
          value={formatTimestampUTC(cap.expiresAt)}
          mono
        />
      </DetailsSection>
    </div>
  );
}

// IssuanceRows shows who asked for the capability and its own revocation
// entry. A capability stopped only through an ancestor has none of its own.
function IssuanceRows({ record }: { record: RecordEntry }) {
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
  const issuerKind = PRINCIPAL_KIND_OPTIONS.find(
    (o) => Number(o.value) === issuer?.kind,
  )?.label;
  const rev = record.revocation;
  return (
    <>
      <DetailRow
        label="Issued by"
        value={
          issuer?.subject
            ? issuerKind
              ? `${issuer.subject} (${issuerKind})`
              : issuer.subject
            : "—"
        }
        mono
        breakAll
      />
      {rev ? (
        <>
          <DetailRow
            label="Revoked"
            value={formatTimestampUTC(rev.revokedAt)}
            mono
          />
          <DetailRow
            label="Revoked by"
            value={rev.actor || "—"}
            mono
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
      <dl className="grid grid-cols-[140px_1fr] gap-x-3 gap-y-1.5 text-sm">
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
