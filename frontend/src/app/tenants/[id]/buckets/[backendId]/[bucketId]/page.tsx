"use client";

// Bucket Overview — read-only metadata pulled from BucketContext
// (already fetched by the bucket-detail layout) plus quick-link
// cards that route to the heavy tabs (Lifecycle, Policy, …).
//
// Conscious choice: surface the resource_name verbatim so
// operators copy-pasting it into CLI/audit-log queries get
// exactly the string the backend uses. The badges above the
// header already convey the human-friendly form.

import Link from "next/link";
import { ClockIcon, ShieldCheckIcon } from "@heroicons/react/24/outline";

import { Card } from "@/components/ui/Card";
import { PublicReadBadge } from "@/components/features/buckets/PublicReadBadge";
import { Badge } from "@/components/ui/badge";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { useTenant } from "../../../tenant-context";
import { useBucket } from "./bucket-context";

export default function BucketOverviewPage() {
  const tenant = useTenant();
  const { bucket } = useBucket();

  const ruleCount = bucket.lifecycleRules.length;
  const enabledCount = bucket.lifecycleRules.filter((r) => r.enabled).length;
  const detailBase = `/tenants/${tenant.slug}/buckets/${encodeURIComponent(
    bucket.backendId,
  )}/${encodeURIComponent(bucket.bucketId)}`;

  return (
    <div className="space-y-4">
      <Card className="p-6">
        <div className="space-y-3">
          <div className="flex items-center gap-2">
            <h2 className="text-lg font-semibold">Identity</h2>
            <ProvisionStateBadge state={bucket.provisionState} />
          </div>
          <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
            <Field label="Resource name">
              <code className={cn(T.code, "break-all")}>{bucket.name}</code>
            </Field>
            <Field label="Backend">
              <code className={T.code}>{bucket.backendId}</code>
            </Field>
            <Field label="Bucket name">
              <code className={T.code}>{bucket.bucketId}</code>
            </Field>
            <Field label="Display name">
              {bucket.displayName || (
                <span className="text-muted-foreground italic">—</span>
              )}
            </Field>
            <Field label="Region">
              {bucket.region ? (
                <code className={T.code}>{bucket.region}</code>
              ) : (
                <span className="text-muted-foreground italic">—</span>
              )}
            </Field>
            <Field label="Owner tenant">
              {bucket.ownerTenantId ? (
                <code className={T.code}>{bucket.ownerTenantId}</code>
              ) : (
                <span className="text-muted-foreground italic">
                  shared (no owner)
                </span>
              )}
            </Field>
            <Field label="Public read">
              {bucket.publicRead ? (
                <PublicReadBadge />
              ) : (
                <span className="text-muted-foreground">private</span>
              )}
            </Field>
            {bucket.publicRead && (
              <Field label="Public base URL">
                {bucket.publicBaseUrl ? (
                  <code className={cn(T.code, "break-all")}>
                    {bucket.publicBaseUrl}
                  </code>
                ) : (
                  <span className="text-muted-foreground italic">
                    the backend&apos;s public endpoint
                  </span>
                )}
              </Field>
            )}
            {bucket.publicRead && (
              <Field label="Allowed content types">
                <code className={T.code}>
                  {bucket.constraints?.allowedContentTypes.join(", ") || "—"}
                </code>
              </Field>
            )}
            <Field label="Resource version">
              <code className={T.code}>{bucket.resourceVersion || "—"}</code>
            </Field>
          </dl>
        </div>
      </Card>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <QuickLink
          href={`${detailBase}/lifecycle`}
          icon={<ClockIcon className="size-5 text-chart-2" />}
          title="Lifecycle"
          subtitle={
            ruleCount === 0
              ? "No rules configured"
              : `${enabledCount} of ${ruleCount} rule${ruleCount === 1 ? "" : "s"} enabled`
          }
        />
        <QuickLink
          href={`${detailBase}/policy`}
          icon={<ShieldCheckIcon className="size-5 text-chart-4" />}
          title="Policy"
          subtitle={
            bucket.cedarPolicy ? "Bucket-level overlay set" : "No overlay"
          }
        />
      </div>
    </div>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <dt className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </dt>
      <dd className="mt-1 break-words">{children}</dd>
    </div>
  );
}

function QuickLink({
  href,
  icon,
  title,
  subtitle,
}: {
  href: string;
  icon: React.ReactNode;
  title: string;
  subtitle: string;
}) {
  return (
    <Link
      href={href}
      className="block rounded-lg border border-border/60 bg-card p-4 transition-colors hover:border-foreground/30 hover:bg-card/80"
    >
      <div className="flex items-center gap-3">
        <div className="flex size-9 items-center justify-center rounded-md bg-muted/40 ring-1 ring-border/50">
          {icon}
        </div>
        <div className="min-w-0">
          <p className="font-medium text-foreground">{title}</p>
          <p className="truncate text-xs text-muted-foreground">{subtitle}</p>
        </div>
      </div>
    </Link>
  );
}

// Mirrors the badge in the cross-tenant /buckets table — kept
// inline rather than extracted because the two surfaces have
// already drifted (color tokens; "ready" emphasis differs) and
// premature deduping would freeze the design.
function ProvisionStateBadge({ state }: { state: string }) {
  const s = state || "ready";
  switch (s) {
    case "ready":
      return (
        <Badge variant="success" className={T.code}>
          ready
        </Badge>
      );
    case "pending":
      return (
        <Badge variant="info" className={T.code}>
          provisioning…
        </Badge>
      );
    case "deleting":
      return (
        <Badge variant="warning" className={T.code}>
          deleting…
        </Badge>
      );
    case "failed":
    case "deletion_failed":
      return (
        <Badge variant="destructive" className={T.code}>
          {s.replace("_", " ")}
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" className={T.code}>
          {s}
        </Badge>
      );
  }
}
