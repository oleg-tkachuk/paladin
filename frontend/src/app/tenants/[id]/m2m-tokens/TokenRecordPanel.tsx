"use client";

import { TrashIcon } from "@heroicons/react/24/outline";

import { DetailPanel } from "@/components/ui/DetailPanel";
import { RecordField, RecordFields } from "@/components/ui/RecordDialog";
import { Button } from "@/components/ui/button";
import { Timestamp } from "@/components/Timestamp";
import type { APIToken } from "@/gen/paladin/admin/v1/api_token_service_pb";
import { T } from "@/lib/ui/typography";

import { isRevoked, tokenLifecycle } from "./_constants";
import { LifecycleBadge } from "@/components/ui/LifecycleBadge";
import { Duration } from "@/components/Duration";

// The share of its cap past which a token's usage reads as a warning.
const NEAR_CAP_RATIO = 0.9;
const MS_PER_SECOND = 1000;

// Per-token sliding-window usage snapshot keyed by token.id; "never" ⇒
// the token was never verified (GetUsage NotFound).
export type UsageSnap = {
  /** The cap in rpm; undefined when the token has none. */
  limitRpm: number | undefined;
  weighted: number;
  currentBucket: bigint;
  resetsAtMs: number;
};

/** A token's place in its lifecycle, as the table and its record show it. */
export function TokenStatus({ token }: { token: APIToken }) {
  return <LifecycleBadge state={tokenLifecycle(token)} className={T.code} />;
}

/**
 * The token's use of its rate limit in the current window, as read at
 * readAtMs. undefined is a reading still loading; a revoked token is not
 * counted.
 */
export function TokenUsage({
  revoked,
  usage,
  readAtMs,
}: {
  revoked: boolean;
  usage: UsageSnap | "never" | undefined;
  readAtMs: number;
}) {
  if (revoked) return <span className="text-muted-foreground">—</span>;
  if (usage === undefined) {
    return <span className="text-muted-foreground">…</span>;
  }
  if (usage === "never") {
    return <span className="text-muted-foreground">never used</span>;
  }
  const resetsInS = Math.max(
    0,
    Math.round((usage.resetsAtMs - readAtMs) / MS_PER_SECOND),
  );
  const limit = usage.limitRpm;
  // A cap of 0 is at its limit from the start.
  const overCap =
    limit !== undefined && usage.weighted >= limit * NEAR_CAP_RATIO;
  return (
    <div className="space-y-0.5">
      <div className={overCap ? "text-warning" : ""}>
        {usage.weighted.toFixed(1)}
        <span className="text-muted-foreground">
          {" / "}
          {limit !== undefined ? limit : "∞"}
        </span>
      </div>
      <div className={T.hint}>resets in {resetsInS}s</div>
    </div>
  );
}

/**
 * One M2M token with every column the table hides on a narrow screen, in a
 * panel beside the list, with Revoke along the bottom while it is live.
 */
export function TokenRecordPanel({
  token,
  usage,
  readAtMs,
  onClose,
  onRevoke,
}: {
  token: APIToken | null;
  usage: UsageSnap | "never" | undefined;
  readAtMs: number;
  onClose: () => void;
  /** Asks to revoke the token shown; the page confirms it. */
  onRevoke: (token: APIToken) => void;
}) {
  return (
    <DetailPanel
      open={token !== null}
      onClose={onClose}
      rowId={token?.id}
      title={token?.displayName || "M2M token"}
      identifier={token ? `paladin_pat_${token.prefix}…` : undefined}
      footer={
        token && !isRevoked(token) ? (
          <Button
            variant="destructive"
            size="sm"
            onClick={() => onRevoke(token)}
          >
            <TrashIcon className="size-4" />
            Revoke
          </Button>
        ) : null
      }
    >
      {token ? (
        <RecordFields>
          <RecordField label="Status">
            <TokenStatus token={token} />
          </RecordField>
          <RecordField label="Token ID" mono>
            {token.id}
          </RecordField>
          <RecordField label="Scopes" mono>
            {token.scopes.join(" ") || "tenant-wide"}
          </RecordField>
          <RecordField label="Audience" mono>
            {token.audience.join(" ") || "—"}
          </RecordField>
          <RecordField label="Roles" mono>
            {token.roles.join(" ") || "—"}
          </RecordField>
          <RecordField label="Rate limit" mono>
            {token.rateLimit.case === "rateLimitRpm"
              ? `${token.rateLimit.value} rpm`
              : "unlimited"}
          </RecordField>
          <RecordField label="Usage" mono>
            <TokenUsage
              revoked={isRevoked(token)}
              usage={usage}
              readAtMs={readAtMs}
            />
          </RecordField>
          <RecordField label="Created">
            {token.createdAt ? <Timestamp ts={token.createdAt} /> : "—"}
            {token.createdBy ? (
              <span className={T.code}> by {token.createdBy}</span>
            ) : null}
          </RecordField>
          <RecordField label="Last used">
            {token.lastUsedAt ? <Timestamp ts={token.lastUsedAt} /> : "never"}
          </RecordField>
          <RecordField label="Expires">
            {token.expiresAt ? <Timestamp ts={token.expiresAt} /> : "never"}
          </RecordField>
          <RecordField label="Valid for">
            {token.expiresAt ? (
              <Duration from={token.createdAt} to={token.expiresAt} />
            ) : (
              "no expiry"
            )}
          </RecordField>
          {token.revokedAt ? (
            <>
              <RecordField label="Revoked">
                <Timestamp ts={token.revokedAt} />
              </RecordField>
              <RecordField label="Revoked after">
                <Duration from={token.createdAt} to={token.revokedAt} />
              </RecordField>
            </>
          ) : null}
        </RecordFields>
      ) : null}
    </DetailPanel>
  );
}
