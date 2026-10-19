"use client";

import React, { useCallback, useEffect, useState } from "react";
import {
  sessionTenantLabel,
  shortTenantId,
} from "@/lib/tenant/sessionTenantLabel";
import { useRouter } from "next/navigation";
import {
  ArrowPathIcon,
  BuildingOfficeIcon,
  CheckIcon,
  ChevronUpDownIcon,
  Cog6ToothIcon,
} from "@heroicons/react/24/outline";

import { useScope } from "@/context/ScopeContext";
import { useAuth } from "@/context/AuthContext";
import { canUseAdminPlane } from "@/constants/roles";
import { useMemberships, type Membership } from "@/hooks/useMemberships";
import { useNotification } from "@/components/ui/Notification";
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
import { FOCUS_RING } from "@/lib/ui/states";

/** The trigger's accessible name starts with this; the e2e suite finds it so. */
export const TENANT_SWITCHER_LABEL = "Tenant switcher";
/** What the list says when the memberships could not be read. */
export const MEMBERSHIPS_FAILED = "Tenant list could not be loaded.";
/** What the trigger says before the tenant: whose session this is, not which
 *  tenant the page shows (ADR-0033). */
export const SIGNED_IN_TO = "Signed in to";
/** The list's heading while the memberships load, and once they have. */
const HEADING_LOADING = "Loading…";
const HEADING_TENANTS = "Your tenants";

/**
 * TenantSwitcher shows which tenant the session is signed in to and switches
 * it. The list is the subject's memberships (AuthService.ListMyMemberships,
 * loaded when the popover opens); picking one calls SwitchTenant, which
 * re-mints the session for that tenant. The signed-in tenant and disabled
 * memberships are inert.
 *
 * It replaced a picker that also held a backend and a bucket. Nothing in the
 * console read either choice, so they changed only the picker's own label.
 */
export const TenantSwitcher: React.FC = () => {
  const router = useRouter();
  const { tenantId, tenant } = useScope();
  const { user, switchTenant } = useAuth();
  const adminPlane = !user || canUseAdminPlane(user.roles);
  const { memberships, loading, error, load } = useMemberships();
  const { showNotification } = useNotification();
  const [open, setOpen] = useState(false);
  const [switching, setSwitching] = useState<string | null>(null);

  // Loaded on open: an operator who never switches pays nothing.
  useEffect(() => {
    if (open) void load();
  }, [open, load]);

  // The token's slug stands in when the tenant record is not readable — a
  // principal without the admin audience never loads it.
  const tenantLabel =
    sessionTenantLabel({
      displayName: tenant?.displayName,
      slug: user?.tenantSlug,
      id: tenantId,
    }) ?? "no tenant";

  const doSwitch = useCallback(
    async (targetId: string) => {
      if (targetId === tenantId || switching) return;
      setSwitching(targetId);
      setOpen(false);
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
    [tenantId, switching, switchTenant, showNotification],
  );

  // Before the memberships arrive, the signed-in tenant alone, so the list is
  // never empty while it loads.
  const rows: Membership[] =
    memberships.length > 0
      ? memberships
      : tenantId
        ? [
            {
              tenantId,
              tenantSlug: user?.tenantSlug ?? "",
              roles: [],
              disabled: false,
              current: true,
            },
          ]
        : [];

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={`${TENANT_SWITCHER_LABEL} — ${SIGNED_IN_TO.toLowerCase()} ${tenantLabel}`}
          className={cn(
            "group inline-flex h-9 max-w-64 items-center gap-2 rounded-md border border-input bg-muted/40 px-3 text-left transition-colors",
            "hover:bg-muted/60",
            FOCUS_RING,
            open && "ring-2 ring-ring",
          )}
        >
          <Avatar className="size-5 shrink-0 rounded-md">
            <AvatarFallback className="rounded-md bg-primary text-sm font-semibold text-primary-foreground">
              {tenantMonogram(tenantLabel)}
            </AvatarFallback>
          </Avatar>
          <span className="flex min-w-0 flex-col leading-tight">
            <span className={cn(T.hint, "hidden sm:block")}>
              {SIGNED_IN_TO}
            </span>
            <span className={cn(T.body, "min-w-0 truncate font-medium")}>
              {tenantLabel}
            </span>
          </span>
          <ChevronUpDownIcon className="size-4 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        side="bottom"
        sideOffset={8}
        className="w-[min(340px,calc(100vw-1.5rem))] p-0"
      >
        <Command shouldFilter>
          <CommandInput placeholder="Search tenants…" />
          <CommandList>
            <CommandEmpty>No tenants match.</CommandEmpty>
            <CommandGroup
              heading={
                loading && memberships.length === 0
                  ? HEADING_LOADING
                  : HEADING_TENANTS
              }
            >
              {rows.map((m) => {
                const isCurrent = m.current || m.tenantId === tenantId;
                // The signed-in row carries the same name as the trigger.
                const name = isCurrent
                  ? tenantLabel
                  : m.tenantSlug?.trim() || shortTenantId(m.tenantId);
                return (
                  <CommandItem
                    key={m.tenantId}
                    value={`${name} ${m.tenantSlug} ${m.tenantId}`}
                    disabled={m.disabled}
                    onSelect={() => {
                      if (isCurrent) setOpen(false);
                      else void doSwitch(m.tenantId);
                    }}
                    className="gap-2 py-2"
                  >
                    <BuildingOfficeIcon className="size-4 shrink-0 text-muted-foreground" />
                    <div className="min-w-0 flex-1">
                      <div
                        className={cn(
                          "truncate text-sm",
                          m.disabled && "text-muted-foreground line-through",
                        )}
                      >
                        {name}
                        {switching === m.tenantId && (
                          <span className="text-muted-foreground">
                            {" "}
                            (switching…)
                          </span>
                        )}
                        {m.disabled && (
                          <span className="text-muted-foreground">
                            {" "}
                            (disabled)
                          </span>
                        )}
                      </div>
                      {m.roles.length > 0 && (
                        <div className="truncate text-sm text-muted-foreground">
                          {m.roles.join(", ")}
                        </div>
                      )}
                    </div>
                    {isCurrent && (
                      <CheckIcon
                        aria-label="Signed in"
                        className="size-4 shrink-0 text-primary"
                      />
                    )}
                  </CommandItem>
                );
              })}
            </CommandGroup>
            {error && (
              <>
                <CommandSeparator />
                <CommandGroup>
                  <p
                    role="alert"
                    className="px-2 py-1.5 text-sm text-destructive"
                  >
                    {MEMBERSHIPS_FAILED}
                  </p>
                  <CommandItem
                    value="__retry__"
                    onSelect={() => void load()}
                    className="gap-2"
                  >
                    <ArrowPathIcon className="size-4" />
                    Retry
                  </CommandItem>
                </CommandGroup>
              </>
            )}
            {/* The tenants page is admin-plane; without the audience the
                link would only lead to a refusal. */}
            {adminPlane && (
              <>
                <CommandSeparator />
                <CommandGroup>
                  <CommandItem
                    value="__manage__"
                    onSelect={() => {
                      setOpen(false);
                      router.push("/tenants");
                    }}
                    className="gap-2"
                  >
                    <Cog6ToothIcon className="size-4" />
                    Manage tenants…
                  </CommandItem>
                </CommandGroup>
              </>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
};

// Two-letter monogram from a name, falling back to "?" when it is blank.
function tenantMonogram(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return "?";
  return (
    trimmed
      .split(/[\s-]+/)
      .slice(0, 2)
      .map((s) => s[0])
      .join("")
      .toUpperCase()
      .slice(0, 2) || "?"
  );
}
