"use client";

import { useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  HomeIcon,
  CubeIcon,
  CloudArrowUpIcon,
  UsersIcon,
  ArchiveBoxIcon,
  TagIcon,
  TrashIcon,
  ClipboardDocumentListIcon,
  Cog6ToothIcon,
  KeyIcon,
  ShieldCheckIcon,
  CheckCircleIcon,
  UserCircleIcon,
  ChevronDoubleLeftIcon,
  ChevronDoubleRightIcon,
  ArrowRightOnRectangleIcon,
  CubeTransparentIcon,
  BanknotesIcon,
  CommandLineIcon,
  BoltIcon,
} from "@heroicons/react/24/outline";

import { cn } from "@/lib/utils";
import { useAuth } from "@/context/AuthContext";
import { useSidebarCounts, SidebarCounts } from "@/hooks/useSidebarCounts";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import {
  TooltipRoot as Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/Tooltip";

/**
 * Each group claims one of the chart-N tokens. The icon picks up that
 * tint so the operator can locate sections at a glance without making
 * the whole row noisy. The active item still uses primary blue + a
 * left-rail accent — see `SidebarBody` below.
 */
const navigationGroups: Array<{
  title: string;
  /** Tailwind class on the icon foreground (idle state). */
  accent: string;
  items: Array<{
    name: string;
    path: string;
    icon: React.ElementType;
    countKey?: keyof SidebarCounts;
  }>;
}> = [
  {
    title: "Core",
    accent: "text-primary/80",
    items: [
      { name: "Dashboard", path: "/", icon: HomeIcon },
      {
        name: "Objects",
        path: "/objects",
        icon: CubeIcon,
        countKey: "objects" as keyof SidebarCounts,
      },
      { name: "Upload", path: "/upload", icon: CloudArrowUpIcon },
    ],
  },
  {
    title: "Management",
    accent: "text-chart-2/85",
    items: [
      {
        name: "Tenants",
        path: "/tenants",
        icon: UsersIcon,
        countKey: "tenants" as keyof SidebarCounts,
      },
      {
        name: "S3 Buckets",
        path: "/buckets",
        icon: ArchiveBoxIcon,
        countKey: "buckets" as keyof SidebarCounts,
      },
      {
        name: "Object Keys",
        path: "/object-keys",
        icon: ArchiveBoxIcon,
        countKey: "objectKeys" as keyof SidebarCounts,
      },
      {
        name: "Object Tags",
        path: "/object-tags",
        icon: TagIcon,
        countKey: "objectTags" as keyof SidebarCounts,
      },
      {
        name: "Trash",
        path: "/trash",
        icon: TrashIcon,
        countKey: "trash" as keyof SidebarCounts,
      },
      { name: "Policies", path: "/policies", icon: ShieldCheckIcon },
      { name: "Tenant Budgets", path: "/tenant-budgets", icon: BanknotesIcon },
      { name: "Billing", path: "/billing", icon: BoltIcon },
    ],
  },
  {
    title: "System",
    accent: "text-chart-3/85",
    items: [
      { name: "Audit Logs", path: "/audit", icon: ClipboardDocumentListIcon },
      { name: "Events", path: "/events", icon: BoltIcon },
      { name: "MCP Bridge", path: "/mcp", icon: CommandLineIcon },
      { name: "Health Status", path: "/health", icon: CheckCircleIcon },
    ],
  },
  {
    title: "Settings",
    accent: "text-chart-5/85",
    items: [
      { name: "Profile", path: "/profile", icon: UserCircleIcon },
      { name: "Personal Tokens", path: "/api-tokens", icon: KeyIcon },
      { name: "M2M Tokens", path: "/m2m-tokens", icon: KeyIcon },
      { name: "Capabilities", path: "/capabilities", icon: ShieldCheckIcon },
      { name: "Configuration", path: "/config", icon: Cog6ToothIcon },
    ],
  },
];

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
          collapsed ? "w-[68px]" : "w-64",
        )}
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
  const counts = useSidebarCounts();

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

  const isCurrent = (path: string) =>
    path === "/" ? pathname === "/" : pathname.startsWith(path);

  return (
    <div className="flex h-full w-full flex-col">
      {/* Brand */}
      <div className="flex h-14 shrink-0 items-center border-b border-sidebar-border px-4">
        <Link href="/" className="flex min-w-0 items-center gap-2.5">
          <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <CubeTransparentIcon className="size-5" />
          </div>
          {!collapsed && (
            <div className="min-w-0 leading-tight">
              <div className="text-sm font-semibold tracking-tight">PALADIN</div>
              <div className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                Control Plane
              </div>
            </div>
          )}
        </Link>
      </div>

      {/* Scope picker now lives in the TopBar (see TopBar.tsx) so the
          breadcrumb and trigger sit alongside the search and user menu.
          The sidebar header keeps the tenantId-keyed layout below it
          intact via `key={tenantId}` on the parent component. */}

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
          {navigationGroups.map((group, groupIdx) => (
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
                    "px-2 pb-1.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground",
                    groupIdx > 0 && "mt-7",
                  )}
                >
                  {group.title}
                </div>
              )}
              {group.items.map((item) => {
                const active = isCurrent(item.path);
                const Icon = item.icon;
                const count =
                  "countKey" in item && item.countKey
                    ? counts[item.countKey]
                    : undefined;

                const link = (
                  <Link
                    key={item.name}
                    href={item.path}
                    onClick={onNavigate}
                    className={cn(
                      // Layout — left rail consumes 2px on the inside
                      "group relative flex h-9 items-center gap-3 rounded-md pr-2 text-sm transition-colors",
                      collapsed ? "justify-center pl-0" : "pl-3",
                      "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                      // Active = brighter sidebar-accent + primary-tinted bg
                      active
                        ? "bg-primary/10 text-foreground font-medium"
                        : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground",
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
                          : cn(group.accent, "group-hover:text-foreground"),
                      )}
                    />
                    {!collapsed && (
                      <>
                        <span className="truncate">{item.name}</span>
                        {count != null && (
                          <span
                            className={cn(
                              "ml-auto rounded px-1.5 py-0.5 font-mono text-[10px] tabular-nums ring-1",
                              active
                                ? "bg-primary/20 text-primary ring-primary/30"
                                : "bg-muted text-muted-foreground ring-transparent",
                            )}
                          >
                            {count.toLocaleString()}
                          </span>
                        )}
                      </>
                    )}
                  </Link>
                );

                return collapsed ? (
                  <Tooltip key={item.name}>
                    <TooltipTrigger asChild>{link}</TooltipTrigger>
                    <TooltipContent side="right">
                      {item.name}
                      {count != null && (
                        <span className="ml-2 font-mono text-muted-foreground">
                          {count.toLocaleString()}
                        </span>
                      )}
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
      <div className="flex shrink-0 items-center justify-between gap-2 border-t border-sidebar-border p-3">
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
              <div className="truncate text-xs font-medium">{displayName}</div>
              <div className="truncate text-[10px] text-muted-foreground">
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
    </div>
  );
}
