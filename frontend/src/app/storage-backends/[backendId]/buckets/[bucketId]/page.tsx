"use client";

// A bucket's overview: what it is, who owns it, and who it is shared with.
// Its settings are the other tabs; what each tenant stores is Collections.

import Link from "next/link";
import {
  ClockIcon,
  FolderIcon,
  ShieldCheckIcon,
  IdentificationIcon,
} from "@heroicons/react/24/outline";

import { Card, CardTitle, CardHeader, CardContent } from "@/components/ui/Card";
import { failedRead } from "@/components/ui/ListLoadError";
import { BucketGrantsCard } from "@/components/features/buckets/BucketGrantsCard";
import { ProvisionStateBadge } from "@/components/features/buckets/ProvisionStateBadge";
import { PublicReadBadge } from "@/components/features/buckets/PublicReadBadge";
import { useTenants } from "@/hooks/useTenants";
import { backendHref, bucketHref, tenantHref } from "@/lib/navigation/hrefs";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { useBucket } from "./bucket-context";

/** What a missing optional field shows. */
const NONE = "—";

export default function BucketOverviewPage() {
  const { bucket, owner, refetch } = useBucket();
  const {
    tenants,
    error: tenantsError,
    refetch: refetchTenants,
  } = useTenants();

  const ruleCount = bucket.lifecycleRules.length;
  const enabledCount = bucket.lifecycleRules.filter((r) => r.enabled).length;
  const tab = (slug: string) =>
    bucketHref(bucket.backendId, bucket.bucketId, slug);
  const ownerName = owner?.displayName || owner?.slug || bucket.ownerTenantId;

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex flex-row items-center gap-2">
          <CardTitle
            className="text-lg font-semibold"
            icon={IdentificationIcon}
          >
            Identity
          </CardTitle>
          <ProvisionStateBadge state={bucket.provisionState} />
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
            <Field label="Resource name">
              <code className={cn(T.code, "break-all")}>{bucket.name}</code>
            </Field>
            <Field label="Owner">
              <Link
                href={tenantHref(owner?.slug || bucket.ownerTenantId)}
                title={bucket.ownerTenantId}
                className="hover:text-primary"
              >
                {ownerName}
              </Link>
            </Field>
            <Field label="Backend">
              <Link
                href={backendHref(bucket.backendId)}
                className={cn(T.code, "hover:text-primary")}
              >
                {bucket.backendId}
              </Link>
            </Field>
            {bucket.displayName && (
              <Field label="Display name">{bucket.displayName}</Field>
            )}
            {bucket.region && (
              <Field label="Region">
                <code className={T.code}>{bucket.region}</code>
              </Field>
            )}
            <Field label="Access">
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
                  {bucket.constraints?.allowedContentTypes.join(", ") || NONE}
                </code>
              </Field>
            )}
            <Field label="Resource version">
              <code className={T.code}>{bucket.resourceVersion || NONE}</code>
            </Field>
          </dl>
        </CardContent>
      </Card>

      <BucketGrantsCard
        bucketName={bucket.name}
        ownerTenantId={bucket.ownerTenantId}
        tenants={tenants}
        tenantsFailed={failedRead(tenantsError, refetchTenants)}
        onChanged={() => void refetch()}
      />

      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
        <QuickLink
          href={tab("collections")}
          icon={<FolderIcon className="size-5 text-chart-5" />}
          title="Collections"
          subtitle="What each tenant stores here"
        />
        <QuickLink
          href={tab("lifecycle")}
          icon={<ClockIcon className="size-5 text-chart-2" />}
          title="Lifecycle"
          subtitle={
            ruleCount === 0
              ? "No rules configured"
              : `${enabledCount} of ${ruleCount} rule${ruleCount === 1 ? "" : "s"} enabled`
          }
        />
        <QuickLink
          href={tab("policy")}
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
      <dt className={T.label}>{label}</dt>
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
