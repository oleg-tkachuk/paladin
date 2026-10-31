"use client";

import { SidebarResourceLinks } from "@/components/layout/ResourceLinks";
import { useScope } from "@/context/ScopeContext";
import { KeyChord } from "@/components/ui/KeyChord";
import { useActingTenant } from "@/hooks/useActingTenant";
import { chordFor, tenantBasePath } from "@/lib/navShortcuts";
import { sessionTenantLabel } from "@/lib/tenant/sessionTenantLabel";
import { useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  SIDEBAR_COLLAPSED_ATTR,
  SIDEBAR_WIDTH,
} from "@/components/layout/sidebarWidth";
import {
  HomeIcon,
  CloudArrowUpIcon,
  UsersIcon,
  ClipboardDocumentListIcon,
  Cog6ToothIcon,
  ShieldCheckIcon,
  ChartBarSquareIcon,
  CheckCircleIcon,
  UserCircleIcon,
  ChevronDoubleLeftIcon,
  ChevronDoubleRightIcon,
  ArrowRightOnRectangleIcon,
  CubeTransparentIcon,
  CommandLineIcon,
  WrenchScrewdriverIcon,
  KeyIcon,
  LinkIcon,
  CurrencyDollarIcon,
  BanknotesIcon,
  BoltIcon,
  CpuChipIcon,
  ServerStackIcon,
  ArchiveBoxIcon,
  TrashIcon,
  FolderIcon,
  QueueListIcon,
  ShareIcon,
} from "@heroicons/react/24/outline";

import { cn } from "@/lib/utils";
import { useAuth } from "@/context/AuthContext";
import { canUseAdminPlane } from "@/constants/roles";
import { routeNeedsAdminPlane } from "@/lib/adminPlaneRoutes";
import { BuildVersion } from "@/components/layout/BuildVersion";
import { ACCOUNT_PAGES } from "@/lib/navigation/account";
import { SECTION, sectionFor } from "@/lib/navigation/sections";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import {
  TooltipRoot as Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/Tooltip";
import {
  FOCUS_RING,
  SIDEBAR_SELECTED_SURFACE,
  SIDEBAR_HOVER_SURFACE,
} from "@/lib/ui/states";

/**
 * Icons stay neutral (muted-foreground) at rest and brighten to the
 * foreground on hover; only the active row carries the brand accent —
 * a primary-tinted icon plus a left-rail marker (see `SidebarBody`).
 * One accent, used sparingly, keeps the shell calm instead of turning
 * the nav into a colour-coded key.
 *
 * `path` is for absolute routes that don't depend on the signed-in
 * user; `tenantTab` is a per-user shortcut that resolves to
 * /tenants/<my-slug>/<tab> at render time. Tenant-scoped sidebar
 * entries (Capabilities, M2M Tokens, Budget, Events) use the
 * latter so they always land on the operator's own tenant subtree.
 * Items with `tenantTab` are hidden until `useAuth` exposes a
 * tenantId/slug, so a not-yet-authenticated user doesn't see broken
 * placeholders.
 */
type NavItem = {
  name: string;
  icon: React.ElementType;
} & ({ path: string; tenantTab?: never } | { path?: never; tenantTab: string });

/** A sidebar row as rendered: where it links, and whether that is a page of
 *  the session tenant's workspace rather than a platform section. */
export interface NavTarget {
  href: string;
  tenantTab: boolean;
}

/**
 * The row to highlight for pathname (ADR-0033). A shortcut into the session
 * tenant's workspace wins on its own pages; otherwise the platform section
 * that owns the page does — a bucket highlights Buckets wherever its URL
 * sits. One row at most, so the workspace's Tenants row and its shortcut are
 * never lit together.
 */
export function activeNavHref(
  pathname: string,
  targets: readonly NavTarget[],
): string | null {
  const shortcut = targets.find(
    (t) =>
      t.tenantTab && (pathname === t.href || pathname.startsWith(`${t.href}/`)),
  );
  if (shortcut) return shortcut.href;
  const section = sectionFor(pathname);
  return targets.find((t) => !t.tenantTab && t.href === section)?.href ?? null;
}

/** The group of shortcuts into the session tenant's workspace. */
export const SESSION_TENANT_GROUP = "Your tenant";

/** The group of the signed-in user's own pages. */
export const ACCOUNT_GROUP = "Account";

// The line under that group's heading naming the tenant: the heading keeps
// the short label every group has, and a long name — or the id, when nothing
// names the tenant — is cut to the sidebar's width rather than wrapping the
// heading over three lines.
const SESSION_TENANT_NAME_CLASS =
  "truncate px-2 pb-1.5 text-sm font-medium text-sidebar-foreground";

const navigationGroups: Array<{
  title: string;
  items: Array<NavItem>;
}> = [
  {
    // The flat Objects entry was removed in Phase 5 — object
    // listing now requires a tenant + Collection scope (lives at
    // /tenants/<id>/collections/<name>/objects, reached via
    // Resources). Upload stays in Core because it doesn't need a
    // pre-selected OK and is the most-used Core entry-point.
    title: "Core",
    items: [
      { name: "Dashboard", path: "/", icon: HomeIcon },
      { name: "Upload", path: "/upload", icon: CloudArrowUpIcon },
    ],
  },
  {
    // Management — collapsed to a single "Resources" entry per Phase 4
    // of the URL refactor. /tenants is the gateway; drilldown into
    // Buckets / Collections / Quotas / Capabilities / Budget / Audit /
    // Events lives under /tenants/<id>/<tab>. The cross-tenant flat
    // indexes (/buckets, /collections) still exist for platform-admin
    // oversight — reachable via Cmd+K — but they're not in the sidebar
    // anymore so the primary path is unambiguous.
    //
    // Policies stays as its own row: it's the system-wide Cedar editor,
    // not a per-tenant view (the per-tenant graph lives under the
    // tenant's Policies tab).
    title: "Management",
    items: [
      {
        // Storage Backends — physical S3-compatible endpoints (AWS,
        // R2, MinIO, SeaweedFS) registered with the platform. Buckets
        // FK into this table; tenants pick one of these as their
        // default binding at creation.
        name: "Storage Backends",
        path: "/storage-backends",
        icon: ServerStackIcon,
      },
      {
        // Buckets — cross-tenant flat list of S3 buckets owned across
        // backends. The per-tenant view lives at
        // /tenants/<slug>/buckets; this is the platform-admin index.
        name: "Buckets",
        path: "/buckets",
        icon: ArchiveBoxIcon,
      },
      {
        // Collections — every tenant's, the platform-admin catalogue; a
        // tenant's own are under its workspace.
        name: "Collections",
        path: SECTION.collections,
        icon: FolderIcon,
      },
      {
        name: "Tenants",
        path: "/tenants",
        icon: UsersIcon,
      },
      {
        // Topology — the rows above as one picture: backends, the buckets
        // on them and the tenants each bucket serves.
        name: "Topology",
        path: SECTION.topology,
        icon: ShareIcon,
      },
      { name: "Users", path: "/users", icon: UserCircleIcon },
      { name: "Policies", path: "/policies", icon: ShieldCheckIcon },
      { name: "Trash", path: "/trash", icon: TrashIcon },
    ],
  },
  {
    // Shortcuts into the session tenant's workspace: Capabilities, M2M
    // tokens, Budget and Events resolve to /tenants/<signed-in>/<tab>, so an
    // operator managing their own tenant need not go through Tenants first.
    // Shown under that tenant's name (sessionTenantGroupTitle); every row is
    // a page of its workspace, and nothing platform-wide belongs here.
    title: SESSION_TENANT_GROUP,
    items: [
      {
        name: "Capabilities",
        tenantTab: "capabilities",
        icon: ShieldCheckIcon,
      },
      { name: "M2M Tokens", tenantTab: "m2m-tokens", icon: CpuChipIcon },
      { name: "Tenant Budget", tenantTab: "budget", icon: BanknotesIcon },
      {
        name: "Events",
        tenantTab: "event-subscriptions",
        icon: BoltIcon,
      },
    ],
  },
  {
    // The MCP server: what agents connected to it are doing, what they may
    // call, how they authorize and how to connect one. A group of its own
    // rather than a row of System — four questions an operator asks at
    // different times, which one page answered only below the fold. Named
    // MCP, not Agents: an agent's capabilities and M2M tokens are a tenant's
    // and stay in its workspace (ADR-0033).
    title: "MCP",
    items: [
      { name: "Overview", path: SECTION.mcp, icon: CommandLineIcon },
      { name: "Tools", path: SECTION.mcpTools, icon: WrenchScrewdriverIcon },
      { name: "Access", path: SECTION.mcpAccess, icon: KeyIcon },
      { name: "Connect", path: SECTION.mcpConnect, icon: LinkIcon },
    ],
  },
  {
    title: "System",
    items: [
      // Platform Statistics — cross-tenant census (tenants / backends /
      // buckets / collections / users + objects by state). Sits above
      // Health because "what do we hold" is the question operators open
      // the System group for; Health answers "is it up".
      { name: "Platform Stats", path: "/stats", icon: ChartBarSquareIcon },
      // Billing spans every tenant: a platform page, not the session tenant's.
      { name: "Billing", path: "/billing", icon: CurrencyDollarIcon },
      { name: "Audit Logs", path: "/audit", icon: ClipboardDocumentListIcon },
      // Batch work as it runs — the drawer's list, with room for detail.
      { name: "Operations", path: SECTION.operations, icon: QueueListIcon },
      {
        name: "Health Status",
        path: "/health",
        icon: CheckCircleIcon,
      },
      // The platform's configuration, not the user's: a page of the admin
      // plane like the rest of System.
      { name: "Configuration", path: SECTION.config, icon: Cog6ToothIcon },
    ],
  },
  {
    // The signed-in user's own pages — the account, and what acts as them —
    // and nothing of a tenant's or the platform's. Last, as a user menu is.
    title: ACCOUNT_GROUP,
    items: ACCOUNT_PAGES.map(({ name, path, icon }) => ({ name, path, icon })),
  },
];

/**
 * The groups to show for these roles. A principal IAM will not issue the
 * admin audience gets only what works without it — everything else would
 * fail on its first request. Until the user is known (null) nothing is taken
 * away, so an operator's sidebar does not flash short on load.
 */
// The product name over its "Control Plane" line, a size above it: at the
// same 14px the name looked the smaller of the two.
export const BRAND_NAME_CLASS = "block text-base font-semibold tracking-tight";

// A group's label (CORE, MANAGEMENT, …). text-sm is a size cn() keeps:
// text-tiny here was dropped as a colour clash with text-muted-foreground,
// and the label rendered at the inherited 16px, above its own items.
export const GROUP_LABEL_CLASS =
  "px-2 pb-1.5 text-sm font-semibold uppercase tracking-wider text-muted-foreground";

export function visibleNavigationGroups(roles: readonly string[] | null) {
  if (roles === null || canUseAdminPlane(roles)) return navigationGroups;
  return navigationGroups
    .map((g) => ({
      ...g,
      // A tenantTab item always lands under /tenants, on the admin plane.
      items: g.items.filter((i) => i.path && !routeNeedsAdminPlane(i.path)),
    }))
    .filter((g) => g.items.length > 0);
}

interface SidebarProps {
  isOpen: boolean;
  onClose: () => void;
}

/**
 * Sidebar — desktop fixed nav + Sheet on mobile.
 *
 * The same `<SidebarBody>` renders in both modes; the Sheet wrapper just
 * gives us the mobile slide-in & overlay for free.
 */
export function Sidebar({ isOpen, onClose }: SidebarProps) {
  const [collapsed, setCollapsed] = useState(false);

  return (
    <>
      {/* Desktop — sticky so it stays in view while body scrolls naturally */}
      <aside
        className={cn(
          "hidden lg:flex shrink-0 border-r border-sidebar-border bg-sidebar text-sidebar-foreground",
          "sticky top-0 h-screen self-start",
          "transition-[width] duration-200 ease-out",
          collapsed ? SIDEBAR_WIDTH.collapsed : SIDEBAR_WIDTH.expanded,
        )}
        {...{ [SIDEBAR_COLLAPSED_ATTR]: collapsed ? "" : undefined }}
      >
        <SidebarBody
          collapsed={collapsed}
          onToggleCollapse={() => setCollapsed((v) => !v)}
          onNavigate={() => {}}
        />
      </aside>

      {/* Mobile */}
      <Sheet open={isOpen} onOpenChange={(o) => !o && onClose()}>
        <SheetContent
          side="left"
          className="w-72 p-0 border-r border-sidebar-border bg-sidebar text-sidebar-foreground"
        >
          <SheetTitle className="sr-only">Navigation</SheetTitle>
          <SidebarBody collapsed={false} onNavigate={onClose} />
        </SheetContent>
      </Sheet>
    </>
  );
}

function SidebarBody({
  collapsed,
  onToggleCollapse,
  onNavigate,
}: {
  collapsed: boolean;
  onToggleCollapse?: () => void;
  onNavigate: () => void;
}) {
  const pathname = usePathname();
  const router = useRouter();
  const { user, logout } = useAuth();

  const displayName = user?.displayName || user?.subject || "—";
  const initials =
    (displayName || "—")
      .trim()
      .split(/\s+/)
      .slice(0, 2)
      .map((s) => s[0])
      .join("")
      .toUpperCase()
      .slice(0, 2) || "—";

  const handleLogout = async () => {
    try {
      await logout();
    } finally {
      router.push("/login");
    }
  };

  // Resolve a tenantTab item to its absolute URL using the
  // signed-in user's tenant. Returns null when auth hasn't loaded
  // yet — caller hides the entry to avoid a broken link.
  //
  // Prefer the slug — it comes through WhoAmIResponse.tenant_slug
  // and lands directly in the AuthUser. Falling back to tenantId
  // (UUID) covers legacy sessions minted before the slug-claim
  // wiring; TenantLayout's resolver canonicalises those at landing
  // via replaceState.
  const tenantHandle = user?.tenantSlug || user?.tenantId || null;
  const { tenant } = useScope();
  // The g-jump that lands on a row, named beside it; the tenant on screen
  // decides where the scoped jumps go, as the handler does.
  const jumpBase = tenantBasePath(useActingTenant());
  const sessionTenantName = sessionTenantLabel({
    displayName: tenant?.displayName,
    slug: user?.tenantSlug,
    id: user?.tenantId,
  });
  const resolveItemHref = (item: NavItem): string | null => {
    if (item.path) return item.path;
    if (!tenantHandle) return null;
    return `/tenants/${encodeURIComponent(tenantHandle)}/${item.tenantTab}`;
  };
  const groups = visibleNavigationGroups(user?.roles ?? null);
  const activeHref = activeNavHref(
    pathname,
    groups.flatMap((g) =>
      g.items.flatMap((item) => {
        const href = resolveItemHref(item);
        return href ? [{ href, tenantTab: !item.path }] : [];
      }),
    ),
  );
  const isCurrent = (href: string) => href === activeHref;

  return (
    <div className="flex h-full w-full flex-col">
      {/* Brand */}
      <div className="flex h-14 shrink-0 items-center border-b border-sidebar-border px-4">
        <div className="flex min-w-0 items-center gap-2.5">
          {/* The logo and the name both go home; the line under the name is
              the running release, which links to /health. Siblings rather
              than one link, since a link cannot hold another. */}
          <Link
            href="/"
            aria-label="Paladin home"
            tabIndex={collapsed ? undefined : -1}
            className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground"
          >
            <CubeTransparentIcon className="size-5" />
          </Link>
          {!collapsed && (
            <div className="min-w-0 leading-tight">
              <Link href="/" className={BRAND_NAME_CLASS}>
                Paladin
              </Link>
              <div className="text-sm font-medium uppercase tracking-wider text-muted-foreground">
                Control Plane
              </div>
            </div>
          )}
        </div>
      </div>

      {/* The tenant switcher lives in the TopBar (see TopBar.tsx), beside
          the search and user menu. The sidebar header keeps the
          tenantId-keyed layout below it intact via `key={tenantId}` on the
          parent component. */}

      {/*
        Navigation. min-h-0 is load-bearing: without it the flex-1 child
        adopts its natural content height, the parent flex column can't
        shrink it below that, and the bottom of the sidebar (Settings
        section + footer) ends up clipped by the viewport on tall pages
        like /config. With min-h-0 the ScrollArea takes whatever space
        is left and overflow scrolling kicks in.
      */}
      <ScrollArea className="min-h-0 flex-1 paladin-scroll">
        <nav className="px-3 py-4">
          {!collapsed && (
            <SidebarResourceLinks
              onNavigate={onNavigate}
              groupLabelClass={GROUP_LABEL_CLASS}
            />
          )}
          {groups.map((group, groupIdx) => (
            <div key={group.title} className="space-y-0.5">
              {!collapsed && (
                <div
                  className={cn(
                    // Each group except the first gets an explicit
                    // top margin so the previous group's last (often
                    // active-tinted) item can never butt up against
                    // the SETTINGS / SYSTEM / etc. label. Picked over
                    // a parent `space-y-X` because the inter-group
                    // gap is the only spacing concern that needs
                    // calling out — items inside a group keep their
                    // tight space-y-0.5.
                    GROUP_LABEL_CLASS,
                    groupIdx > 0 && "mt-7",
                  )}
                >
                  {group.title}
                </div>
              )}
              {!collapsed &&
              group.title === SESSION_TENANT_GROUP &&
              sessionTenantName ? (
                <div
                  className={SESSION_TENANT_NAME_CLASS}
                  title={user?.tenantId}
                  data-testid="session-tenant-name"
                >
                  {sessionTenantName}
                </div>
              ) : null}
              {group.items.map((item) => {
                const href = resolveItemHref(item);
                // Tenant-scoped item without a known tenant — hide
                // rather than render a broken `/tenants//capabilities`
                // placeholder. Restored after auth resolves.
                if (href === null) return null;
                const active = isCurrent(href);
                const Icon = item.icon;
                const chord = chordFor(href, jumpBase);
                const link = (
                  <Link
                    key={item.name}
                    href={href}
                    onClick={onNavigate}
                    className={cn(
                      // Layout — left rail consumes 2px on the inside
                      "group relative flex h-9 items-center gap-3 rounded-md pr-2 text-sm transition-colors",
                      collapsed ? "justify-center pl-0" : "pl-3",
                      FOCUS_RING,
                      // Active = the accent's tint filled in, and the rail
                      active
                        ? cn(
                            SIDEBAR_SELECTED_SURFACE,
                            "text-foreground font-medium",
                          )
                        : cn(
                            SIDEBAR_HOVER_SURFACE,
                            "text-muted-foreground hover:text-foreground",
                          ),
                    )}
                    aria-current={active ? "page" : undefined}
                  >
                    {/* Left rail accent — visible on active row only */}
                    {active && !collapsed && (
                      <span
                        aria-hidden
                        className="absolute inset-y-1 left-0 w-0.5 rounded-full bg-primary"
                      />
                    )}
                    <Icon
                      className={cn(
                        "size-4 shrink-0 transition-colors",
                        active
                          ? "text-primary"
                          : "text-muted-foreground group-hover:text-foreground",
                      )}
                    />
                    {!collapsed && (
                      <>
                        <span className="truncate">{item.name}</span>
                        {chord && (
                          <KeyChord
                            chord={chord}
                            className="ml-auto hidden group-hover:flex group-focus-visible:flex"
                          />
                        )}
                      </>
                    )}
                  </Link>
                );

                return collapsed ? (
                  <Tooltip key={item.name}>
                    <TooltipTrigger asChild>{link}</TooltipTrigger>
                    <TooltipContent
                      side="right"
                      className="flex items-center gap-2"
                    >
                      {item.name}
                      {chord && <KeyChord chord={chord} />}
                    </TooltipContent>
                  </Tooltip>
                ) : (
                  link
                );
              })}
            </div>
          ))}
        </nav>
      </ScrollArea>

      {/* Footer — real signed-in user + explicit Logout button. The
          previous design hardcoded "AD / Admin / System Operator"
          which looked fine in a static screenshot but lied about who
          was actually using the cluster. Logout now lives both here
          (always-visible) and in the topbar avatar dropdown. */}
      <div className="shrink-0 border-t border-sidebar-border p-3">
        <div className="flex items-center justify-between gap-2">
          <Link
            href="/profile"
            className={cn(
              "group/me flex min-w-0 items-center gap-2 rounded-md transition-colors hover:text-foreground",
              collapsed && "justify-center w-full",
            )}
          >
            <div className="flex size-7 shrink-0 items-center justify-center rounded-full bg-secondary text-xs font-semibold text-secondary-foreground group-hover/me:bg-primary group-hover/me:text-primary-foreground">
              {initials}
            </div>
            {!collapsed && (
              <div className="min-w-0">
                <div className="truncate text-xs font-medium">
                  {displayName}
                </div>
                <div className="truncate text-sm text-muted-foreground">
                  {user?.roles?.[0] || (user ? "no role" : "not signed in")}
                </div>
              </div>
            )}
          </Link>
          {!collapsed && (
            <Tooltip>
              <TooltipTrigger asChild>
                <button
                  type="button"
                  onClick={handleLogout}
                  aria-label="Sign out"
                  className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
                >
                  <ArrowRightOnRectangleIcon className="size-4" />
                </button>
              </TooltipTrigger>
              <TooltipContent side="top">Sign out</TooltipContent>
            </Tooltip>
          )}
          {onToggleCollapse && (
            <button
              onClick={onToggleCollapse}
              className="hidden rounded-md p-1.5 text-muted-foreground hover:bg-sidebar-accent hover:text-foreground lg:inline-flex"
              aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            >
              {collapsed ? (
                <ChevronDoubleRightIcon className="size-4" />
              ) : (
                <ChevronDoubleLeftIcon className="size-4" />
              )}
            </button>
          )}
        </div>
        {/* The running release, after Argo CD's: under who is signed in, at
          the foot of the navigation, rather than on every page. */}
        {!collapsed && (
          <div className="mt-2 pl-1">
            <BuildVersion />
          </div>
        )}
      </div>
    </div>
  );
}
