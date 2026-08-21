"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import {
  ArchiveBoxIcon,
  BuildingOfficeIcon,
  CheckIcon,
  ChevronUpDownIcon,
  PlusIcon,
  ServerStackIcon,
} from "@heroicons/react/24/outline";

import { useScope } from "@/context/ScopeContext";
import { useAuth } from "@/context/AuthContext";
import { useMemberships } from "@/hooks/useMemberships";
import { useNotification } from "@/components/ui/Notification";
import { useBuckets } from "@/hooks/useBuckets";
import { useBackends } from "@/hooks/useBackends";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command";

/**
 * ScopePicker — unified locator for the operator inside the control
 * plane. Replaces the old <TenantSwitcher>; consumes the merged
 * useScope() so the trigger label, the cascading rows, and the
 * PageHeader breadcrumbs all read from the same source of truth.
 *
 * Trigger:
 *   A single TopBar button that shows the active triple
 *   `(backend / bucket / tenant)` as a breadcrumb-style label. Width
 *   responsive — collapses to just the tenant name on small screens.
 *
 * Panel (cascading rows, in data-hierarchy order):
 *   Backend  → BackendService.ListBackends. Soft scope, persisted.
 *              Picking one invalidates the bucket selection.
 *   Bucket   → filtered to the active backend. Picking one auto-pivots
 *              backend via setScope so the two coordinates stay
 *              consistent.
 *   Tenant   → Switcher. Lists every tenant the subject is a member of
 *              (AuthService.ListMyMemberships); selecting one calls
 *              SwitchTenant, which re-mints the session for it (a new
 *              refresh family). The signed-in + disabled memberships are
 *              inert. The active tenant still comes from
 *              useAuth().user.tenantId via ScopeContext.
 *
 * The panel is also opened programmatically by clicking a segment in
 * the PageHeader breadcrumb — see openScopePicker() on ScopeContext.
 */
export const ScopePicker: React.FC = () => {
  const router = useRouter();

  const {
    tenantId,
    tenant,
    backendId,
    bucketId,
    setBackend,
    setBucket,
    setScope,
    isPickerOpen,
    setPickerOpen,
  } = useScope();

  const { switchTenant } = useAuth();
  const {
    memberships,
    loading: membershipsLoading,
    load: loadMemberships,
  } = useMemberships();
  const { showNotification } = useNotification();
  const [switching, setSwitching] = useState<string | null>(null);

  // Fetch the tenant list lazily when the picker opens — an operator who
  // never switches pays nothing.
  useEffect(() => {
    if (isPickerOpen) void loadMemberships();
  }, [isPickerOpen, loadMemberships]);

  const doSwitch = useCallback(
    async (targetId: string) => {
      if (!targetId || targetId === tenantId || switching) return;
      setSwitching(targetId);
      setPickerOpen(false);
      try {
        await switchTenant(targetId);
        showNotification({
          type: "success",
          title: "Tenant switched",
          message: "Your session is now scoped to the selected tenant.",
        });
      } catch (e) {
        showNotification({
          type: "error",
          title: "Switch failed",
          message: (e as Error).message,
        });
      } finally {
        setSwitching(null);
      }
    },
    [tenantId, switching, switchTenant, showNotification, setPickerOpen],
  );

  const { buckets, fetchBuckets, loading: bucketsLoading } = useBuckets();
  const { backends: backendRows } = useBackends();

  // Backends come from BackendService.ListBackends — same source the
  // /buckets and /collections pickers use, so the picker stays
  // consistent with what's actually FK-valid.
  const backends = useMemo(
    () => backendRows.map((b) => b.backendId),
    [backendRows],
  );

  // Refetch buckets whenever the backend scope changes, so the bucket
  // picker only shows valid options.
  useEffect(() => {
    fetchBuckets(backendId || undefined);
  }, [backendId, fetchBuckets]);

  // Cascade — narrow the bucket list to the active backend (defence
  // in depth: the server-side filter above already does this, but we
  // briefly hold a stale list while a refetch is in flight).
  const visibleBuckets = useMemo(
    () =>
      backendId ? buckets.filter((b) => b.backendId === backendId) : buckets,
    [buckets, backendId],
  );

  // Tenant cascade — when a bucket is picked, the bucket's owner
  // tenant becomes the canonical scope for the picker. Falls back to
  // the JWT tenant when the bucket is shared (owner is empty) or no
  // bucket is selected. We can't actually switch the auth tenant in
  // 1:1 mode, so this just controls what the row displays + lets the
  // operator confirm "this bucket is owned by N".
  const selectedBucket = useMemo(
    () => visibleBuckets.find((b) => b.bucketId === bucketId) ?? null,
    [visibleBuckets, bucketId],
  );
  const scopedOwnerTenantId =
    selectedBucket && selectedBucket.ownerTenantId
      ? selectedBucket.ownerTenantId
      : null;
  const tenantMismatch =
    !!scopedOwnerTenantId && !!tenantId && scopedOwnerTenantId !== tenantId;

  const tenantLabel =
    tenant?.displayName?.trim() ||
    (tenantId ? `${tenantId.slice(0, 8)}…` : "no tenant");

  // Tenant switcher rows. Once memberships load, one row per tenant the
  // subject belongs to (the current one marked, disabled ones inert,
  // switchable ones call doSwitch). Before they load we show just the
  // signed-in tenant so the row is never empty. A bucket-owner row is appended
  // (informational, no-op) when the selected bucket is owned by a different
  // tenant than the session.
  const tenantItems = useMemo<ScopeRowItem[]>(() => {
    const rows: ScopeRowItem[] =
      memberships.length > 0
        ? memberships.map((m) => {
            const isCurrent = m.current || m.tenantId === tenantId;
            const label =
              m.tenantSlug?.trim() ||
              `${m.tenantId.slice(0, 8)}…${m.tenantId.slice(-4)}`;
            const inert = isCurrent || m.disabled;
            return {
              key: m.tenantId,
              searchValue: `${m.tenantSlug} ${m.tenantId} ${m.roles.join(" ")}`,
              isActive: isCurrent,
              onSelect: inert ? () => {} : () => void doSwitch(m.tenantId),
              render: () => (
                <>
                  <BuildingOfficeIcon
                    className={cn(
                      "size-4 shrink-0 text-muted-foreground",
                      m.disabled && "opacity-50",
                    )}
                  />
                  <div className="min-w-0 flex-1">
                    <div
                      className={cn(
                        "truncate text-sm",
                        m.disabled && "text-muted-foreground line-through",
                      )}
                    >
                      {label}{" "}
                      {isCurrent && (
                        <span className="text-[10px] text-muted-foreground">
                          (signed in)
                        </span>
                      )}
                      {switching === m.tenantId && (
                        <span className="text-[10px] text-muted-foreground">
                          (switching…)
                        </span>
                      )}
                    </div>
                    <div className="truncate font-mono text-[10px] text-muted-foreground">
                      {m.tenantId}
                      {m.roles.length > 0 ? ` · ${m.roles.join(", ")}` : ""}
                    </div>
                  </div>
                  {m.disabled && (
                    <span className="shrink-0 rounded-sm bg-destructive/10 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-destructive">
                      Disabled
                    </span>
                  )}
                </>
              ),
            };
          })
        : tenantId
          ? [
              {
                key: tenantId,
                searchValue: `${tenant?.displayName ?? ""} ${tenantId}`,
                isActive: true,
                onSelect: () => {},
                render: () => (
                  <>
                    <BuildingOfficeIcon className="size-4 shrink-0 text-muted-foreground" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm">
                        {tenant?.displayName?.trim() || "Untitled tenant"}{" "}
                        <span className="text-[10px] text-muted-foreground">
                          (signed in)
                        </span>
                      </div>
                      <div className="truncate font-mono text-[10px] text-muted-foreground">
                        {tenantId}
                      </div>
                    </div>
                  </>
                ),
              },
            ]
          : [];

    if (scopedOwnerTenantId && tenantMismatch) {
      rows.push({
        key: `__owner__${scopedOwnerTenantId}`,
        searchValue: scopedOwnerTenantId,
        isActive: false,
        onSelect: () => {},
        render: () => (
          <>
            <BuildingOfficeIcon className="size-4 shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm">
                Bucket owner{" "}
                <span className="text-[10px] text-muted-foreground">
                  (scope)
                </span>
              </div>
              <div className="truncate font-mono text-[10px] text-muted-foreground">
                {scopedOwnerTenantId}
              </div>
            </div>
          </>
        ),
      });
    }
    return rows;
  }, [
    memberships,
    tenantId,
    tenant,
    switching,
    doSwitch,
    scopedOwnerTenantId,
    tenantMismatch,
  ]);

  return (
    <Popover open={isPickerOpen} onOpenChange={setPickerOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={`Scope picker — backend ${backendId ?? "any"}, bucket ${bucketId ?? "any"}, tenant ${tenantLabel}`}
          className={cn(
            "group inline-flex h-9 max-w-[420px] items-center gap-2 rounded-md border border-input bg-muted/40 px-3 text-left transition-colors",
            "hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            isPickerOpen && "ring-2 ring-ring",
          )}
        >
          <Avatar className="size-5 shrink-0 rounded-md">
            <AvatarFallback className="rounded-md bg-primary text-[9px] font-semibold text-primary-foreground">
              {tenantMonogram(tenant?.displayName ?? null)}
            </AvatarFallback>
          </Avatar>
          {/* Full breadcrumb on md+ */}
          <span className="hidden min-w-0 items-center gap-1.5 md:inline-flex">
            <span
              className={cn(
                T.codeSmall,
                "max-w-[110px] truncate",
                !backendId && "italic text-muted-foreground",
              )}
            >
              {backendId ?? "any"}
            </span>
            <span className={cn(T.hint, "shrink-0")}>·</span>
            <span
              className={cn(
                T.codeSmall,
                "max-w-[120px] truncate",
                !bucketId && "italic text-muted-foreground",
              )}
            >
              {bucketId ?? "any"}
            </span>
            <span className={cn(T.hint, "shrink-0")}>·</span>
            <span className={cn(T.body, "max-w-[140px] truncate font-medium")}>
              {tenantLabel}
            </span>
          </span>
          {/* Compact: tenant only */}
          <span
            className={cn(
              T.body,
              "min-w-0 max-w-[140px] truncate font-medium md:hidden",
            )}
          >
            {tenantLabel}
          </span>
          <ChevronUpDownIcon className="size-4 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        side="bottom"
        sideOffset={8}
        className="w-[min(360px,calc(100vw-1.5rem))] p-0"
      >
        <div className="space-y-2 px-3 py-3">
          {/* ── Backend picker ─────────────────────────────────────────── */}
          <ScopeRow
            label="Backend"
            icon={ServerStackIcon}
            primary={backendId ?? "Any backend"}
            secondary={
              backends.length === 0
                ? "no backends configured"
                : `${backends.length} configured`
            }
            muted={!backendId}
            searchPlaceholder="Search backends…"
            emptyMessage="No backends match."
            groupHeading="Backends"
            items={[
              {
                key: "__any__",
                searchValue: "any all backends",
                isActive: !backendId,
                onSelect: () => setBackend(null),
                render: () => (
                  <>
                    <ServerStackIcon className="size-4 shrink-0 text-muted-foreground" />
                    <span className="flex-1 text-sm italic text-muted-foreground">
                      Any backend
                    </span>
                  </>
                ),
              },
              ...backendRows.map((row) => {
                const id = row.backendId;
                // Disabled backends (feature 002) are shown with a badge
                // and are NOT selectable as a working scope — selecting one
                // would only lead to FailedPrecondition on the next op.
                const disabled = !row.enabled;
                return {
                  key: id,
                  searchValue: id,
                  isActive: backendId === id,
                  onSelect: disabled ? () => {} : () => setBackend(id),
                  render: () => (
                    <>
                      <ServerStackIcon
                        className={cn(
                          "size-4 shrink-0 text-muted-foreground",
                          disabled && "opacity-50",
                        )}
                      />
                      <span
                        className={cn(
                          "flex-1 truncate font-mono text-sm",
                          disabled && "text-muted-foreground line-through",
                        )}
                      >
                        {id}
                      </span>
                      {disabled && (
                        <span className="shrink-0 rounded-sm bg-destructive/10 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-destructive">
                          Disabled
                        </span>
                      )}
                    </>
                  ),
                };
              }),
            ]}
            footerItems={[
              {
                key: "manage",
                label: "Inspect server config…",
                icon: PlusIcon,
                onSelect: () => router.push("/config"),
              },
            ]}
          />

          {/* ── Bucket picker ──────────────────────────────────────────── */}
          <ScopeRow
            label="Bucket"
            icon={ArchiveBoxIcon}
            primary={bucketId ?? "Any bucket"}
            secondary={
              visibleBuckets.length === 0
                ? backendId
                  ? `no buckets in ${backendId}`
                  : "no buckets visible"
                : `${visibleBuckets.length} visible${
                    backendId ? ` in ${backendId}` : ""
                  }`
            }
            muted={!bucketId}
            loading={bucketsLoading && visibleBuckets.length === 0}
            searchPlaceholder="Search buckets…"
            emptyMessage="No buckets match."
            groupHeading={backendId ? `Buckets in ${backendId}` : "Buckets"}
            items={[
              {
                key: "__any__",
                searchValue: "any all buckets",
                isActive: !bucketId,
                onSelect: () => setBucket(null),
                render: () => (
                  <>
                    <ArchiveBoxIcon className="size-4 shrink-0 text-muted-foreground" />
                    <span className="flex-1 text-sm italic text-muted-foreground">
                      Any bucket
                    </span>
                  </>
                ),
              },
              ...visibleBuckets.map((b) => ({
                key: `${b.backendId}/${b.bucketId}`,
                searchValue: `${b.bucketId} ${b.displayName || ""} ${b.backendId}`,
                isActive: bucketId === b.bucketId,
                onSelect: () => setScope(b.backendId, b.bucketId),
                render: () => (
                  <>
                    <ArchiveBoxIcon className="size-4 shrink-0 text-muted-foreground" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-mono text-sm">
                        {b.bucketId}
                      </div>
                      <div className="truncate text-[10px] text-muted-foreground">
                        {b.backendId}
                        {b.displayName ? ` · ${b.displayName}` : ""}
                        {b.ownerTenantId
                          ? ` · owned by ${b.ownerTenantId.slice(0, 8)}…`
                          : " · shared"}
                      </div>
                    </div>
                  </>
                ),
              })),
            ]}
            footerItems={[
              {
                key: "manage",
                label: "Manage buckets…",
                icon: PlusIcon,
                onSelect: () => router.push("/buckets"),
              },
            ]}
          />

          {/* ── Tenant ────────────────────────────────────────────────────
                Now a real switcher: memberships come from
                AuthService.ListMyMemberships (via useMemberships, loaded
                when the picker opens). Selecting another tenant calls
                SwitchTenant, which re-mints the session for it. The
                signed-in tenant + disabled memberships are inert. A
                bucket-owner row is appended (informational) when the
                selected bucket belongs to a different tenant. */}
          <ScopeRow
            label="Tenant"
            icon={BuildingOfficeIcon}
            avatar={
              <Avatar className="size-6 shrink-0 rounded-md">
                <AvatarFallback className="rounded-md bg-primary text-[9px] font-semibold text-primary-foreground">
                  {tenantMonogram(
                    scopedOwnerTenantId
                      ? null /* unknown name — show "?" for the bucket-owner scope */
                      : (tenant?.displayName ?? null),
                  )}
                </AvatarFallback>
              </Avatar>
            }
            primary={
              scopedOwnerTenantId
                ? `${scopedOwnerTenantId.slice(0, 8)}…${scopedOwnerTenantId.slice(-4)}`
                : selectedBucket
                  ? "Shared"
                  : tenant?.displayName?.trim() || "Untitled tenant"
            }
            secondary={
              scopedOwnerTenantId
                ? tenantMismatch
                  ? `signed in as ${tenant?.displayName ?? tenantId?.slice(0, 8)}…`
                  : "scoped via bucket"
                : tenantId
                  ? `${tenantId.slice(0, 8)}…${tenantId.slice(-4)}`
                  : "no tenant"
            }
            muted={!tenantId && !scopedOwnerTenantId}
            loading={membershipsLoading && memberships.length === 0}
            searchPlaceholder="Search tenants…"
            emptyMessage="No tenants match."
            groupHeading="Switch tenant"
            items={tenantItems}
            footerItems={[
              {
                key: "manage",
                label: "Manage tenants…",
                icon: PlusIcon,
                onSelect: () => router.push("/tenants"),
              },
            ]}
          />
        </div>
      </PopoverContent>
    </Popover>
  );
};

// Two-letter monogram from a display name, falling back to "?"
// when the name is blank — same heuristic the previous TenantBadge
// used so the visual identity stays stable.
function tenantMonogram(displayName: string | null): string {
  const name = (displayName || "").trim();
  if (!name) return "?";
  return (
    name
      .split(/\s+/)
      .slice(0, 2)
      .map((s) => s[0])
      .join("")
      .toUpperCase()
      .slice(0, 2) || "?"
  );
}

// ─── ScopeRow ───────────────────────────────────────────────────────────────

interface ScopeRowItem {
  key: string;
  searchValue: string;
  isActive: boolean;
  onSelect: () => void;
  render: () => React.ReactNode;
}

interface ScopeRowFooterItem {
  key: string;
  label: string;
  icon: React.ElementType;
  onSelect: () => void;
}

interface ScopeRowProps {
  label: string;
  icon: React.ElementType;
  avatar?: React.ReactNode;
  primary: string;
  secondary: string;
  muted?: boolean;
  loading?: boolean;
  searchPlaceholder: string;
  emptyMessage: string;
  groupHeading: string;
  items: ScopeRowItem[];
  footerItems?: ScopeRowFooterItem[];
}

/**
 * One row of the picker. Renders a tightly-packed button (icon · label ·
 * value · chevron) and opens a Popover-hosted Command palette on click.
 */
function ScopeRow({
  label,
  icon: Icon,
  avatar,
  primary,
  secondary,
  muted,
  loading,
  searchPlaceholder,
  emptyMessage,
  groupHeading,
  items,
  footerItems,
}: ScopeRowProps) {
  const [open, setOpen] = useState(false);

  const select = (cb: () => void) => {
    cb();
    setOpen(false);
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={cn(
            "group flex w-full items-center gap-2 rounded-md border bg-card/40 px-2 py-1.5 text-left transition-colors",
            "hover:bg-card focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            open && "ring-2 ring-ring",
          )}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-label={`Switch ${label.toLowerCase()} — current: ${primary} (${secondary})`}
          title={`${primary} · ${secondary}`}
        >
          {avatar ?? (
            <div className="flex size-6 shrink-0 items-center justify-center rounded-md bg-secondary text-secondary-foreground">
              <Icon className="size-3.5" />
            </div>
          )}
          <span className="w-[60px] shrink-0 truncate text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
            {label}
          </span>
          <span
            className={cn(
              "min-w-0 flex-1 truncate text-sm leading-tight",
              muted ? "text-muted-foreground italic" : "font-medium",
            )}
          >
            {primary}
          </span>
          <ChevronUpDownIcon className="size-4 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        side="bottom"
        sideOffset={8}
        className="w-[min(320px,calc(100vw-1.5rem))] p-0"
      >
        <Command shouldFilter>
          <CommandInput placeholder={searchPlaceholder} />
          <CommandList>
            <CommandEmpty>{emptyMessage}</CommandEmpty>
            <CommandGroup heading={loading ? "Loading…" : groupHeading}>
              {items.map((it) => (
                <CommandItem
                  key={it.key}
                  value={it.searchValue}
                  onSelect={() => select(it.onSelect)}
                  className="gap-2 py-2"
                >
                  {it.render()}
                  {it.isActive && (
                    <CheckIcon className="size-4 shrink-0 text-primary" />
                  )}
                </CommandItem>
              ))}
            </CommandGroup>
            {footerItems && footerItems.length > 0 && (
              <>
                <CommandSeparator />
                <CommandGroup>
                  {footerItems.map((f) => {
                    const FIcon = f.icon;
                    return (
                      <CommandItem
                        key={f.key}
                        value={`__footer__-${f.key}`}
                        onSelect={() => select(f.onSelect)}
                        className="gap-2"
                      >
                        <FIcon className="size-4" />
                        {f.label}
                      </CommandItem>
                    );
                  })}
                </CommandGroup>
              </>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
