"use client";

import { useEffect, useMemo } from "react";
import {
  ArrowPathIcon,
  ArrowTopRightOnSquareIcon,
  BuildingOfficeIcon,
  CheckCircleIcon,
  ClipboardDocumentIcon,
  CommandLineIcon,
  ExclamationTriangleIcon,
  ServerStackIcon,
  UserCircleIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useNotification } from "@/components/ui/Notification";
import { useAuth } from "@/context/AuthContext";
import { useBackends } from "@/hooks/useBackends";
import { useConfig } from "@/hooks/useConfig";
import { useTenants } from "@/hooks/useTenants";
import { copyToClipboard, cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { ScrollArea } from "@/components/ui/scroll-area";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/Skeleton";

// /config used to render the raw control-plane YAML, but admin/v1.
// SystemService.GetConfig hasn't landed yet — useConfig is a stub
// returning null, which the previous version of this page surfaced
// as an alarming red "Sync failed" panel.
//
// Replace it with what's already live: a cluster snapshot pulled from
// existing admin RPCs (TenantService.ListTenants,
// BackendService.ListBackends) and the signed-in identity from the JWT.
// When SystemService.GetConfig finally lands, we add the YAML viewer back
// as a third card.

// A count whose read failed. "0" and "no tenants yet" were what it showed,
// which is a claim about the platform; the snapshot links to the list page,
// where the failure has its own retry.
const UNKNOWN_COUNT = "—";
const COUNT_FAILED = "could not be loaded";

export default function ConfigPage() {
  const { user } = useAuth();
  const {
    tenants,
    loading: tenantsLoading,
    error: tenantsError,
    fetchTenants,
  } = useTenants();
  const {
    backends,
    loading: backendsLoading,
    error: backendsError,
    fetchBackends,
  } = useBackends();
  const {
    config: yamlBlob,
    path: configPath,
    loading: configLoading,
    error: configError,
    refresh: refreshConfig,
  } = useConfig();
  const { showNotification } = useNotification();

  useEffect(() => {
    void fetchTenants();
  }, [fetchTenants]);

  const handleRefresh = () => {
    void fetchTenants();
    void fetchBackends();
    void refreshConfig();
  };

  const copyConfig = async () => {
    const ok = await copyToClipboard(yamlBlob ?? "");
    showNotification({
      type: ok ? "success" : "error",
      title: ok ? "Copied" : "Copy failed",
      message: ok ? "YAML copied to clipboard." : "Clipboard unavailable.",
    });
  };

  // ── snapshot stats ──────────────────────────────────────────────────
  const tenantCount = tenants.length;
  const backendCount = backends.length;
  const stillLoading =
    (tenantsLoading && tenantCount === 0) ||
    (backendsLoading && backendCount === 0);

  const roles = useMemo(() => user?.roles ?? [], [user]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Configuration"
        description="Cluster snapshot and developer-mode controls."
        showDefaultActions={false}
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={handleRefresh}
            disabled={tenantsLoading || backendsLoading}
          >
            <ArrowPathIcon
              className={
                tenantsLoading || backendsLoading
                  ? "size-4 animate-spin"
                  : "size-4"
              }
            />
            Refresh
          </Button>
        }
      />

      {/* ─── Cluster snapshot ────────────────────────────────────────
            One slim card. The previous design wrapped each datum in a
            framed tile (border + bg-muted/20 + p-4 + 2xl numeric)
            which made three short pieces of info take 200px of
            vertical space. Replaced with a shared definition-list
            grid (label tiny + primary base + inline hint) — same
            data, ~half the area, and visually consistent with the
            /health Overview card. ─────────────────────────────── */}
      <Card>
        <CardHeader className="px-6">
          <CardTitle className="text-base">Cluster snapshot</CardTitle>
          <CardDescription>
            Pulled directly from the control plane on every refresh — no
            caching.
          </CardDescription>
        </CardHeader>
        <Separator />
        <CardContent className="grid grid-cols-2 gap-x-6 gap-y-4 px-6 py-4 sm:grid-cols-3">
          <SnapshotItem
            icon={UserCircleIcon}
            label="Signed in as"
            primary={user?.displayName || user?.subject || "—"}
            secondary={
              user
                ? `${user.tenantId.slice(0, 8)}…${user.tenantId.slice(-4)}`
                : "no session"
            }
            footer={
              roles.length > 0 ? (
                <div className="flex flex-wrap gap-1">
                  {roles.map((r) => (
                    <Badge
                      key={r}
                      variant="outline"
                      className={cn(T.labelTight, "font-mono")}
                    >
                      {r}
                    </Badge>
                  ))}
                </div>
              ) : null
            }
            href={null}
          />
          <SnapshotItem
            icon={BuildingOfficeIcon}
            label="Tenants"
            primary={
              stillLoading ? (
                <Skeleton className="h-5 w-10" />
              ) : tenantsError ? (
                UNKNOWN_COUNT
              ) : (
                String(tenantCount)
              )
            }
            secondary={
              tenantsError
                ? COUNT_FAILED
                : tenantCount === 0
                  ? "no tenants yet"
                  : tenantCount === 1
                    ? "1 workspace"
                    : `${tenantCount} workspaces`
            }
            href="/tenants"
          />
          <SnapshotItem
            icon={ServerStackIcon}
            label="Backends"
            primary={
              stillLoading ? (
                <Skeleton className="h-5 w-10" />
              ) : backendsError ? (
                UNKNOWN_COUNT
              ) : (
                String(backendCount)
              )
            }
            secondary={
              backendsError
                ? COUNT_FAILED
                : backendCount === 0
                  ? "none registered"
                  : backendCount === 1
                    ? "1 storage backend"
                    : `${backendCount} storage backends`
            }
            href="/buckets"
          />
        </CardContent>
      </Card>

      {/* ─── Raw config (admin/v1.SystemService.GetConfig) ───────── */}
      <Card>
        <CardHeader className="flex flex-row items-center gap-3 px-6">
          <div className="flex size-9 items-center justify-center rounded-md bg-primary/15 text-primary ring-1 ring-primary/30">
            <CommandLineIcon className="size-5" />
          </div>
          <div className="flex-1 min-w-0 space-y-0.5">
            <CardTitle className="text-base">
              Raw system configuration
            </CardTitle>
            <CardDescription className={T.code}>
              {configPath || "—"}
            </CardDescription>
          </div>
          {configError ? (
            <Badge variant="destructive" className="gap-1.5 font-normal">
              <ExclamationTriangleIcon className="size-3.5" />
              Error
            </Badge>
          ) : configLoading && !yamlBlob ? (
            <Badge variant="warning" className="gap-1.5 font-normal">
              <ArrowPathIcon className="size-3.5 animate-spin" />
              Loading
            </Badge>
          ) : yamlBlob ? (
            <Badge variant="success" className="gap-1.5 font-normal">
              <CheckCircleIcon className="size-3.5" />
              Live
            </Badge>
          ) : null}
          <Button
            size="sm"
            variant="outline"
            onClick={copyConfig}
            disabled={!yamlBlob}
          >
            <ClipboardDocumentIcon className="size-4" />
            Copy YAML
          </Button>
        </CardHeader>
        <Separator />
        <CardContent className="p-0">
          {configError ? (
            <div className="px-6 py-8 text-sm">
              <p className="font-medium text-destructive">
                Failed to fetch configuration
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                {configError.message}
              </p>
              {/^permission/i.test(configError.message) && (
                <p className="mt-3 text-xs text-muted-foreground">
                  This surface requires the{" "}
                  <code className="rounded bg-muted px-1 font-mono">
                    platform.admin
                  </code>{" "}
                  role.
                </p>
              )}
            </div>
          ) : configLoading && !yamlBlob ? (
            <div className="space-y-2 px-6 py-5">
              <Skeleton className="h-4 w-full" />
              <Skeleton className="h-4 w-5/6" />
              <Skeleton className="h-4 w-3/4" />
              <Skeleton className="h-4 w-full" />
              <Skeleton className="h-4 w-2/3" />
            </div>
          ) : (
            <ScrollArea className="paladin-scroll h-130 w-full">
              <pre className="px-6 py-5 font-mono text-xs leading-relaxed text-foreground/90">
                {yamlBlob}
              </pre>
            </ScrollArea>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

// ─── snapshot item ───────────────────────────────────────────────────
//
// Definition-list cell (label tiny + primary base + secondary mono
// hint). Used directly inside the Cluster-snapshot CardContent's grid
// — no inner box, no double border. When `href` is set, the whole cell
// becomes a link with a discreet "open ↗" affordance to its right.

interface SnapshotItemProps {
  icon: React.ElementType;
  label: string;
  primary: React.ReactNode;
  secondary: string;
  footer?: React.ReactNode;
  href: string | null;
}

function SnapshotItem({
  icon: Icon,
  label,
  primary,
  secondary,
  footer,
  href,
}: SnapshotItemProps) {
  const inner = (
    <div className="space-y-1">
      <div className={cn(T.label, "flex items-center gap-1.5")}>
        <Icon className="size-3.5" />
        {label}
        {href ? (
          <ArrowTopRightOnSquareIcon className="ml-auto size-3 opacity-0 transition-opacity group-hover/snap:opacity-100" />
        ) : null}
      </div>
      <div className="text-base font-semibold leading-tight">{primary}</div>
      <div className={cn(T.code, "text-muted-foreground")}>{secondary}</div>
      {footer ? <div className="pt-0.5">{footer}</div> : null}
    </div>
  );

  if (href) {
    return (
      <a
        href={href}
        className="group/snap block rounded-md transition-colors hover:text-primary"
      >
        {inner}
      </a>
    );
  }
  return inner;
}
