"use client";

// Tenant Overview — landing page for /tenants/<id>.
//
// Composition:
//   - Identity (slug + tenant_id + display).
//   - Counts row: buckets owned, collections provisioned. Each tile
//     links into the matching tab so the number doubles as a CTA.
//   - Budget tile: cap + current-period spend (or "no budget set").
//   - Recent audit: top 5 entries scoped to this tenant, link out to
//     the full Audit tab.
//
// Each block fetches independently and renders best-effort — a budget
// RPC failure shouldn't blank the audit feed and vice versa. Counts
// and audit reuse cross-tenant List RPCs with a tenant filter applied
// client-side (buckets) or server-side (collections via parent,
// audit via filter expression).

import { useEffect, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { ConnectError, Code } from "@connectrpc/connect";
import {
  ArchiveBoxIcon,
  ArrowLeftIcon,
  ArrowRightIcon,
  BanknotesIcon,
  ClipboardDocumentListIcon,
  ServerStackIcon,
  ShieldCheckIcon,
  TagIcon,
} from "@heroicons/react/24/outline";

import { IdentityField } from "@/components/IdentityField";
import { RelativeTime } from "@/components/RelativeTime";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { StorageMigrationCard } from "./StorageMigrationCard";
import { Skeleton } from "@/components/ui/Skeleton";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import {
  bucketClient,
  collectionClient,
  auditClient,
  tenantBudgetClient,
} from "@/lib/connect/client";
import type {
  TenantBudget,
  TenantBudgetServiceGetResponse,
} from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import type { AuditLogEntry } from "@/gen/paladin/admin/v1/types_pb";
import { API_PAGE_SIZE_MAX } from "@/constants";

import { useTenant } from "./tenant-context";

const QUICK_LINKS: Array<{
  label: string;
  href: (slug: string) => string;
  description: string;
  icon: React.ElementType;
}> = [
  {
    label: "Buckets",
    href: (s) => `/tenants/${s}/buckets`,
    description: "S3 buckets owned by this tenant.",
    icon: ArchiveBoxIcon,
  },
  {
    label: "Collections",
    href: (s) => `/tenants/${s}/collections`,
    description: "Tenant-scoped namespaces routed to a bucket.",
    icon: TagIcon,
  },
  {
    label: "Policies",
    href: (s) => `/tenants/${s}/policies`,
    description: "Effective Cedar policy graph.",
    icon: ShieldCheckIcon,
  },
];

// Re-export the shared money formatter so the Budget tile renders
// consistently with /billing and the Tenant Budget tab. The local
// duplicate that used to live here printed bare numbers for the
// UNIT case (no suffix), so a metering-only tenant looked like
// "0 / 1,000" — same digits the cap had. The shared formatter
// emits "0 units / 1,000 units" for that case.
import { formatMoney } from "@/lib/format/money";
import { ActorName } from "@/components/features/audit/ActorName";
import { formatCount } from "@/lib/format/locale";

// IdentityCard renders the tenant's three identity fields in priority
// order — display name as the heading (mutable, human-friendly), slug
// as the immutable handle (operator-friendly), tenant_id as the
// immutable canonical UUID (audit / debug). Lock icons mark immutable
// fields; copy buttons sit beside slug + UUID since those are what
// operators paste into shells, configs, and Cedar policies.
// StorageBreadcrumb reads ?from=storage&backend=X&bucket=Y from the
// URL. When present it renders a small "← Back to bucket Y on X"
// link so an operator who drilled in from /storage-backends/.../
// buckets/Y/ doesn't lose the trail.
function StorageBreadcrumb() {
  const params = useSearchParams();
  if (params.get("from") !== "storage") return null;
  const backend = params.get("backend") || "";
  const bucket = params.get("bucket") || "";
  if (!backend || !bucket) return null;
  return (
    <div className="-mt-2 flex items-center gap-2 text-xs text-muted-foreground">
      <Link
        href={`/storage-backends/${encodeURIComponent(backend)}/buckets/${encodeURIComponent(bucket)}`}
        className="inline-flex items-center gap-1.5 rounded-md border border-border bg-muted/40 px-2 py-1 hover:bg-muted hover:text-foreground"
      >
        <ArrowLeftIcon className="size-3" />
        Back to bucket <span className="font-mono">{bucket}</span> on{" "}
        <span className="font-mono">{backend}</span>
      </Link>
    </div>
  );
}

function IdentityCard({
  tenant,
}: {
  tenant: {
    tenantId: string;
    slug: string;
    displayName: string;
    storageLayout: string;
  };
}) {
  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-baseline justify-between gap-3">
          <CardTitle className="text-lg leading-tight">
            {tenant.displayName || (
              <span className="text-muted-foreground italic">(unnamed)</span>
            )}
          </CardTitle>
          <span className="text-[10px] uppercase tracking-wider text-muted-foreground">
            Tenant
          </span>
        </div>
      </CardHeader>
      <CardContent className="space-y-2 pt-0">
        <IdentityField label="slug" value={tenant.slug} immutable />
        <IdentityField label="id" value={tenant.tenantId} immutable truncate />
        <IdentityField
          label="storage"
          value={tenant.storageLayout || "shared"}
          immutable
        />
      </CardContent>
    </Card>
  );
}

export default function TenantOverviewPage() {
  const tenant = useTenant();

  // ── counts (buckets, collections) ──────────────────────────────
  const [bucketCount, setBucketCount] = useState<number | null>(null);
  const [okCount, setOkCount] = useState<number | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        // Server-side narrow via owner_tenant_id (backed by the
        // partial index from migration 006). Skips the cross-backend
        // scan + client-side filter the previous version did.
        const res = await bucketClient.listBuckets({
          parent: "",
          page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
          filter: "",
          ownerTenantId: tenant.tenantId,
        });
        if (cancelled) return;
        setBucketCount(res.buckets.length);
      } catch {
        if (!cancelled) setBucketCount(0);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [tenant.tenantId]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await collectionClient.listCollections({
          parent: `tenants/${tenant.tenantId}`,
          page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
          filter: "",
        });
        if (cancelled) return;
        setOkCount(res.collections.length);
      } catch {
        if (!cancelled) setOkCount(0);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [tenant.tenantId]);

  // ── budget ────────────────────────────────────────────────────
  const [budget, setBudget] = useState<TenantBudget | null>(null);
  const [budgetLoaded, setBudgetLoaded] = useState(false);
  const [budgetMissing, setBudgetMissing] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res: TenantBudgetServiceGetResponse =
          await tenantBudgetClient.get({ tenantId: tenant.tenantId });
        if (cancelled) return;
        setBudget(res.budget ?? null);
        setBudgetMissing(!res.budget);
      } catch (err) {
        if (cancelled) return;
        // NotFound = no budget configured (a state, not an error);
        // anything else we still surface as "missing" so the tile
        // fails gracefully — the dedicated /tenant-budgets page
        // shows the real diagnostic.
        if (err instanceof ConnectError && err.code === Code.NotFound) {
          setBudgetMissing(true);
        } else {
          setBudgetMissing(true);
        }
      } finally {
        if (!cancelled) setBudgetLoaded(true);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [tenant.tenantId]);

  // ── recent audit (top 5) ──────────────────────────────────────
  const [auditEntries, setAuditEntries] = useState<AuditLogEntry[] | null>(
    null,
  );
  const [auditError, setAuditError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        // CEL filter — match either the actor tenant OR a resource
        // name owned by the tenant. The `||` is required: actions
        // initiated by platform-admin against a tenant resource
        // would otherwise miss the tenant feed.
        const filter =
          `actor_tenant_id == "${tenant.tenantId}" || ` +
          `resource_name.startsWith("tenants/${tenant.tenantId}/")`;
        const res = await auditClient.listAuditLog({
          page: { pageSize: 5, pageToken: "" },
          filter,
        });
        if (cancelled) return;
        setAuditEntries(res.entries);
      } catch (err) {
        if (cancelled) return;
        setAuditError(
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to load audit log",
        );
        setAuditEntries([]);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [tenant.tenantId]);

  return (
    <div className="space-y-4">
      {/* Storage-first context: when the operator landed here from
          `/storage-backends/.../buckets/.../`, show a back-link so
          they can hop back into the bucket browser without losing
          their place in the IA. ?from=storage carries backend+bucket
          so the link is reversible. */}
      <StorageBreadcrumb />

      {/* ─── Identity ──────────────────────────────────────────── */}
      <IdentityCard tenant={tenant} />

      {/* ─── Counts row ────────────────────────────────────────── */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <CountTile
          href={`/tenants/${tenant.slug}/buckets`}
          label="Buckets"
          value={bucketCount}
          icon={ArchiveBoxIcon}
          accent="text-chart-2/85"
          subtitle="owned by this tenant"
        />
        <CountTile
          href={`/tenants/${tenant.slug}/collections`}
          label="Collections"
          value={okCount}
          icon={ServerStackIcon}
          accent="text-chart-4/85"
          subtitle="provisioned namespaces"
        />
        <BudgetTile
          href={`/tenants/${tenant.slug}/budget`}
          loaded={budgetLoaded}
          budget={budget}
          missing={budgetMissing}
        />
      </div>

      {/* ─── Quick links ───────────────────────────────────────── */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {QUICK_LINKS.map(({ label, href, description, icon: Icon }) => (
          <Link
            key={label}
            href={href(tenant.slug)}
            className="group rounded-xl focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <Card className="h-full transition-colors hover:border-primary/40 hover:bg-card/60">
              <CardHeader className="flex flex-row items-start gap-3 px-4">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-secondary">
                  <Icon className="size-5 text-chart-2/85" />
                </div>
                <div className="min-w-0 flex-1 space-y-0.5">
                  <CardTitle className="text-sm">{label}</CardTitle>
                  <p className={cn(T.hint, "leading-snug")}>{description}</p>
                </div>
                <ArrowRightIcon className="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-foreground" />
              </CardHeader>
            </Card>
          </Link>
        ))}
      </div>

      {/* ─── Storage migration (ADR-0015 Phase 3) ──────────────── */}
      <StorageMigrationCard
        tenantId={tenant.tenantId}
        storageLayout={tenant.storageLayout || "shared"}
      />

      {/* ─── Recent activity ───────────────────────────────────── */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base flex items-center gap-2">
            <ClipboardDocumentListIcon className="size-4 text-chart-3" />
            Recent activity
          </CardTitle>
          <Link
            href={`/tenants/${tenant.slug}/audit-log`}
            className="text-xs font-medium text-primary hover:underline"
          >
            View full audit →
          </Link>
        </CardHeader>
        <CardContent className="px-4 pb-4">
          {auditEntries === null ? (
            <div className="space-y-2">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-9 w-full" />
              ))}
            </div>
          ) : auditError ? (
            <p className={cn(T.helper, "text-destructive")}>{auditError}</p>
          ) : auditEntries.length === 0 ? (
            <p className={cn(T.helper, "italic")}>
              No audit entries for this tenant yet.
            </p>
          ) : (
            <ul className="divide-y divide-border/60 -mx-2">
              {auditEntries.map((e) => (
                <AuditRow key={e.entryId} entry={e} />
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

// ─── tiles ──────────────────────────────────────────────────────

function CountTile({
  href,
  label,
  value,
  icon: Icon,
  accent,
  subtitle,
}: {
  href: string;
  label: string;
  value: number | null;
  icon: React.ElementType;
  accent: string;
  subtitle: string;
}) {
  return (
    <Link
      href={href}
      className="group rounded-xl focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <Card className="h-full transition-colors hover:border-primary/40 hover:bg-card/60">
        <CardContent className="flex items-center gap-3 p-4">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-md bg-secondary">
            <Icon className={cn("size-5", accent)} />
          </div>
          <div className="min-w-0 flex-1">
            <div className="text-2xl font-semibold tabular-nums leading-none">
              {value === null ? (
                <Skeleton className="h-7 w-12" />
              ) : (
                formatCount(value)
              )}
            </div>
            <div className="mt-1 flex items-baseline gap-2">
              <span className="text-sm font-medium">{label}</span>
              <span className={cn(T.hint, "leading-none")}>{subtitle}</span>
            </div>
          </div>
          <ArrowRightIcon className="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-foreground" />
        </CardContent>
      </Card>
    </Link>
  );
}

function BudgetTile({
  href,
  loaded,
  budget,
  missing,
}: {
  href: string;
  loaded: boolean;
  budget: TenantBudget | null;
  missing: boolean;
}) {
  const cap = budget?.maxBudgetAmount ?? 0;
  const spent = budget?.spentAmount ?? 0;
  // Default to "UNIT" (abstract metering sentinel) when the
  // budget row has no unit_code — covers freshly-created budgets
  // and tenants doing non-currency metering. Avoids a misleading
  // "$0.00" label on a tenant that doesn't actually pay in USD.
  const unit = budget?.unitCode || "UNIT";
  // Cap of 0 means unlimited per the proto comment; pct only
  // makes sense when there's a finite cap.
  const pct = cap > 0 ? Math.min(100, (spent / cap) * 100) : null;
  const overCap = cap > 0 && spent > cap;

  return (
    <Link
      href={href}
      className="group rounded-xl focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <Card className="h-full transition-colors hover:border-primary/40 hover:bg-card/60">
        <CardContent className="flex items-start gap-3 p-4">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-md bg-secondary">
            <BanknotesIcon className="size-5 text-chart-5/85" />
          </div>
          <div className="min-w-0 flex-1 space-y-1">
            <div className="flex items-baseline gap-2">
              <span className="text-sm font-medium">Budget</span>
              {overCap && (
                <Badge variant="destructive" className={T.labelTight}>
                  over cap
                </Badge>
              )}
              {!overCap && cap === 0 && budget && (
                <Badge variant="outline" className={T.labelTight}>
                  unlimited
                </Badge>
              )}
            </div>

            {!loaded ? (
              <Skeleton className="h-5 w-32" />
            ) : missing ? (
              <p className={cn(T.hint, "italic")}>No budget set</p>
            ) : (
              <>
                <div className="text-sm font-mono tabular-nums">
                  {formatMoney(spent, unit)}
                  {cap > 0 && (
                    <span className="text-muted-foreground">
                      {" "}
                      / {formatMoney(cap, unit)}
                    </span>
                  )}
                </div>
                {pct !== null && (
                  <div className="h-1.5 w-full rounded-full bg-muted overflow-hidden">
                    <div
                      className={cn(
                        "h-full transition-all",
                        overCap
                          ? "bg-destructive"
                          : pct > 80
                            ? "bg-amber-500"
                            : "bg-chart-2",
                      )}
                      style={{ width: `${pct}%` }}
                    />
                  </div>
                )}
              </>
            )}
          </div>
          <ArrowRightIcon className="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-foreground" />
        </CardContent>
      </Card>
    </Link>
  );
}

// ─── audit row ──────────────────────────────────────────────────

function AuditRow({ entry }: { entry: AuditLogEntry }) {
  // Action format is "<service>.<Method>" — e.g.
  // "admin.BucketService.CreateBucket". Show the trailing
  // method in bold + the prefix as muted context so it's
  // skim-friendly.
  const lastDot = entry.action.lastIndexOf(".");
  const method = lastDot >= 0 ? entry.action.slice(lastDot + 1) : entry.action;
  const prefix = lastDot >= 0 ? entry.action.slice(0, lastDot) : "";

  const failed = !!entry.errorMessage;

  return (
    <li className="flex items-start gap-3 px-2 py-2 text-sm">
      <span
        className={cn(
          "mt-1.5 size-1.5 shrink-0 rounded-full",
          failed ? "bg-destructive" : "bg-chart-2",
        )}
        aria-hidden
      />
      <div className="min-w-0 flex-1 space-y-0.5">
        <div className="flex items-baseline gap-2">
          <span className="font-mono text-xs font-medium truncate">
            {method}
          </span>
          {prefix && (
            <span className="text-[10px] uppercase tracking-wider text-muted-foreground truncate">
              {prefix}
            </span>
          )}
          {failed && (
            <Badge variant="destructive" className={T.labelTight}>
              failed
            </Badge>
          )}
        </div>
        <div
          className={cn(
            T.hint,
            "flex flex-wrap items-baseline gap-x-2 truncate",
          )}
        >
          <span className="text-muted-foreground">by</span>
          <span className="text-xs truncate">
            <ActorName
              subject={entry.actorSubject}
              tenantId={entry.actorTenantId}
            />
          </span>
          {entry.resourceName && (
            <>
              <span className="text-muted-foreground">on</span>
              <span className="font-mono text-xs truncate">
                {entry.resourceName}
              </span>
            </>
          )}
        </div>
      </div>
      <RelativeTime
        ts={entry.at}
        className={cn(T.hint, "shrink-0 whitespace-nowrap")}
      />
    </li>
  );
}
