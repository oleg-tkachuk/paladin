"use client";

// A short profile of a tenant, a bucket, a user or a capability, shown when
// the pointer rests on one named in a table: what it is, its state, who owns
// it, and a link to its page. Each reads its resource only once the card
// opens, through the same query options the resource's page reads with
// (lib/queries/details), so the page then opens from the cache.
import Link from "next/link";
import { QueryClientContext, useQuery } from "@tanstack/react-query";
import { useContext, useState } from "react";
import type React from "react";

import { Badge } from "@/components/ui/badge";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@/components/ui/hover-card";
import { Skeleton } from "@/components/ui/Skeleton";
import { RelativeTime } from "@/components/RelativeTime";
import { PrincipalKind } from "@/gen/paladin/admin/v1/capability_service_pb";
import { isProvisionInFlight } from "@/lib/bucketProvision";
import { normalizeError } from "@/lib/connect/error";
import {
  bucketDetailQuery,
  capabilityDetailQuery,
  tenantDetailQuery,
  userDetailQuery,
} from "@/lib/queries/details";
import { isDedicatedLayout } from "@/lib/storageLayout";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

/** How long an opened card's answer is reused before it is read again. */
export const HOVER_CARD_STALE_MS = 30_000;

/** A capability's page: its tenant's list, open on it. */
export function capabilityHref(tenantId: string, id: string): string {
  return `/tenants/${encodeURIComponent(tenantId)}/capabilities?id=${encodeURIComponent(id)}`;
}

/** What a principal's kind is called on a card. */
const PRINCIPAL_KIND: Partial<Record<PrincipalKind, string>> = {
  [PrincipalKind.USER]: "user",
  [PrincipalKind.AGENT]: "agent",
  [PrincipalKind.SERVICE]: "service",
};

/** Whether the card is open, and the options its read takes while it is. */
function useOpen() {
  const [open, setOpen] = useState(false);
  return {
    open,
    setOpen,
    reads: { enabled: open, retry: false, staleTime: HOVER_CARD_STALE_MS },
  };
}

/** The card around a read: a placeholder, the failure, or the profile. */
function Card<D>({
  open,
  onOpenChange,
  trigger,
  what,
  query,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  trigger: React.ReactNode;
  /** The resource, as the failure names it: "Tenant". */
  what: string;
  query: { data?: D; error: Error | null; isPending: boolean };
  children: (data: D) => React.ReactNode;
}) {
  return (
    <HoverCard open={open} onOpenChange={onOpenChange}>
      <HoverCardTrigger asChild>{trigger}</HoverCardTrigger>
      <HoverCardContent aria-label={`${what} profile`}>
        {query.error ? (
          <p className={cn(T.hint, "text-destructive")}>
            {what} could not be read: {normalizeError(query.error).message}
          </p>
        ) : query.isPending || query.data === undefined ? (
          <div
            className="space-y-2"
            aria-label={`Reading the ${what.toLowerCase()}`}
          >
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-3 w-1/2" />
          </div>
        ) : (
          <div className="space-y-2">{children(query.data)}</div>
        )}
      </HoverCardContent>
    </HoverCard>
  );
}

/** A label and its value, one line of a profile. */
function Line({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-baseline justify-between gap-3 text-xs">
      <span className="text-muted-foreground">{label}</span>
      <span className="min-w-0 truncate text-right">{children}</span>
    </div>
  );
}

function Title({ name, id }: { name: string; id: string }) {
  return (
    <div className="min-w-0">
      <p className="truncate font-semibold">{name}</p>
      <p className={cn(T.codeSmall, "truncate text-muted-foreground")}>{id}</p>
    </div>
  );
}

function Open({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Link
      href={href}
      className="text-xs font-medium text-primary hover:underline"
    >
      {children}
    </Link>
  );
}

/** A tenant named by its id or slug. */
function TenantHoverCardInner({
  handle,
  children,
}: {
  handle: string;
  children: React.ReactNode;
}) {
  const { open, setOpen, reads } = useOpen();
  const query = useQuery({ ...tenantDetailQuery(handle), ...reads });
  return (
    <Card
      open={open}
      onOpenChange={setOpen}
      trigger={children}
      what="Tenant"
      query={query}
    >
      {(t) => (
        <>
          <Title name={t.displayName || t.slug} id={t.slug || t.tenantId} />
          <div className="flex flex-wrap gap-1">
            <Badge variant="outline">
              {isDedicatedLayout(t.storageLayout)
                ? "dedicated storage"
                : "shared storage"}
            </Badge>
            {t.deletedAt ? <Badge variant="destructive">in trash</Badge> : null}
          </div>
          <Line label="Created">
            <RelativeTime ts={t.createdAt} />
          </Line>
          <Open href={`/tenants/${encodeURIComponent(t.slug || t.tenantId)}`}>
            Open tenant
          </Open>
        </>
      )}
    </Card>
  );
}

/** A bucket of a backend. */
function BucketHoverCardInner({
  backendId,
  bucketId,
  children,
}: {
  backendId: string;
  bucketId: string;
  children: React.ReactNode;
}) {
  const { open, setOpen, reads } = useOpen();
  const query = useQuery({
    ...bucketDetailQuery(backendId, bucketId),
    ...reads,
  });
  return (
    <Card
      open={open}
      onOpenChange={setOpen}
      trigger={children}
      what="Bucket"
      query={query}
    >
      {(b) => (
        <>
          <Title
            name={b.displayName || b.bucketId}
            id={`${b.backendId}/${b.bucketId}`}
          />
          <div className="flex flex-wrap gap-1">
            <Badge variant="outline">
              {b.publicRead ? "public" : "private"}
            </Badge>
            {isProvisionInFlight(b.provisionState) ? (
              <Badge variant="secondary">{b.provisionState}</Badge>
            ) : null}
          </div>
          <Line label="Owner">
            <span className={T.codeSmall}>{b.ownerTenantId || "—"}</span>
          </Line>
          <Line label="Shared with">
            {b.grantedTenantIds.length === 0
              ? "no tenant"
              : `${b.grantedTenantIds.length} tenant${b.grantedTenantIds.length === 1 ? "" : "s"}`}
          </Line>
          {b.region ? <Line label="Region">{b.region}</Line> : null}
          <Open
            href={`/storage-backends/${encodeURIComponent(b.backendId)}/buckets/${encodeURIComponent(b.bucketId)}`}
          >
            Open bucket
          </Open>
        </>
      )}
    </Card>
  );
}

/** A user, by resource name: "tenants/<tenant>/users/<user>". */
function UserHoverCardInner({
  name,
  children,
}: {
  name: string;
  children: React.ReactNode;
}) {
  const { open, setOpen, reads } = useOpen();
  const query = useQuery({ ...userDetailQuery(name), ...reads });
  return (
    <Card
      open={open}
      onOpenChange={setOpen}
      trigger={children}
      what="User"
      query={query}
    >
      {(u) => (
        <>
          <Title name={u.displayName || u.subject} id={u.subject} />
          <div className="flex flex-wrap gap-1">
            {u.disabled ? (
              <Badge variant="destructive">disabled</Badge>
            ) : (
              <Badge variant="outline">active</Badge>
            )}
            {u.roles.map((r) => (
              <Badge key={r} variant="secondary">
                {r}
              </Badge>
            ))}
          </div>
          <Line label="Last login">
            <RelativeTime ts={u.lastLoginAt} />
          </Line>
          <Open
            href={`/users/${encodeURIComponent(u.tenantId)}/${encodeURIComponent(u.userId)}`}
          >
            Open user
          </Open>
        </>
      )}
    </Card>
  );
}

/** A capability, by id, in the tenant whose page holds it. */
function CapabilityHoverCardInner({
  id,
  tenantId,
  children,
}: {
  id: string;
  tenantId: string;
  children: React.ReactNode;
}) {
  const { open, setOpen, reads } = useOpen();
  const query = useQuery({ ...capabilityDetailQuery(id), ...reads });
  return (
    <Card
      open={open}
      onOpenChange={setOpen}
      trigger={children}
      what="Capability"
      query={query}
    >
      {(record) => {
        const c = record.capability;
        const kind = c?.subject ? PRINCIPAL_KIND[c.subject.kind] : undefined;
        return (
          <>
            <Title
              name={
                c?.subject
                  ? `${kind ?? "principal"} ${c.subject.subject}`
                  : "Capability"
              }
              id={id}
            />
            <div className="flex flex-wrap gap-1">
              {record.revocation ? (
                <Badge variant="destructive">revoked</Badge>
              ) : (
                <Badge variant="outline">not revoked</Badge>
              )}
              {c?.audience.map((a) => (
                <Badge key={a} variant="secondary">
                  {a}
                </Badge>
              ))}
            </div>
            <Line label="Expires">
              {c?.expiresAt ? <RelativeTime ts={c.expiresAt} /> : "never"}
            </Line>
            <Open href={capabilityHref(tenantId, id)}>Open capability</Open>
          </>
        );
      }}
    </Card>
  );
}

/**
 * A card wants the page's query client to read with; a component rendered
 * without one — alone in a test — shows its trigger and no card.
 */
function guarded<P extends { children: React.ReactNode }>(
  Inner: (props: P) => React.ReactNode,
) {
  return function Guarded(props: P) {
    const client = useContext(QueryClientContext);
    return client ? <Inner {...props} /> : <>{props.children}</>;
  };
}

/** A tenant named by its id or slug. */
export const TenantHoverCard = guarded(TenantHoverCardInner);
/** A bucket of a backend. */
export const BucketHoverCard = guarded(BucketHoverCardInner);
/** A user, by resource name: "tenants/<tenant>/users/<user>". */
export const UserHoverCard = guarded(UserHoverCardInner);
/** A capability, by id, in the tenant whose page holds it. */
export const CapabilityHoverCard = guarded(CapabilityHoverCardInner);
