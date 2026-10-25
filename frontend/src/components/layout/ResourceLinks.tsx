"use client";

// The resources the user keeps coming back to: each resource page they open
// is remembered, the star in the top bar pins the one on screen, and the
// sidebar lists the pinned and the recent ones. Stored in their settings
// (useResourceLinks), so they follow them to another browser.
import { useEffect } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  ClockIcon,
  EllipsisHorizontalIcon,
  StarIcon,
} from "@heroicons/react/24/outline";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { StarIcon as StarSolidIcon } from "@heroicons/react/24/solid";

import { Button } from "@/components/ui/button";
import { useCrumbNames } from "@/context/CrumbNamesContext";
import { useResourceLinks } from "@/hooks/useResourceLinks";
import { groupedLinks, resourceOf } from "@/lib/preferences/resourceLinks";
import { cn } from "@/lib/utils";

/** How many links the sidebar lists; the palette lists them all. */
export const SIDEBAR_LINKS_SHOWN = 6;

/** The resource on screen, by the names the page gave its ids. */
function useCurrentResource() {
  const pathname = usePathname();
  const names = useCrumbNames();
  return resourceOf(pathname, names);
}

/** Remembers each resource page as it is opened, by the name it shows. */
export function RecordVisits() {
  const current = useCurrentResource();
  const { visit } = useResourceLinks();
  const href = current?.href;
  const label = current?.label;
  const kind = current?.kind;
  useEffect(() => {
    if (href && label && kind) visit({ href, label, kind });
  }, [href, label, kind, visit]);
  return null;
}

/** Pins or unpins the resource on screen; nothing on other pages. */
export function PinToggle() {
  const current = useCurrentResource();
  const { isPinned, togglePin } = useResourceLinks();
  if (!current) return null;
  const pinned = isPinned(current.href);
  const Icon = pinned ? StarSolidIcon : StarIcon;
  return (
    <Button
      variant="ghost"
      size="icon"
      aria-pressed={pinned}
      aria-label={pinned ? `Unpin ${current.label}` : `Pin ${current.label}`}
      title={pinned ? "Unpin from the sidebar" : "Pin to the sidebar"}
      onClick={() => void togglePin(current).catch(() => undefined)}
    >
      <Icon className={cn("size-5", pinned && "text-primary")} />
    </Button>
  );
}

/** The pinned, then the recent resources, as sidebar links. */
export function SidebarResourceLinks({
  onNavigate,
  groupLabelClass,
}: {
  onNavigate?: () => void;
  groupLabelClass: string;
}) {
  const links = useResourceLinks();
  const { pinned, recent } = links;
  const entries = groupedLinks(pinned, recent).slice(0, SIDEBAR_LINKS_SHOWN);
  if (!links.shown || entries.length === 0) return null;
  return (
    <div className="mb-7 space-y-0.5" data-testid="resource-links">
      <div className="flex items-center justify-between gap-2 pr-1">
        <div className={cn(groupLabelClass, "min-w-0 truncate")}>
          Pinned &amp; recent
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              aria-label="Pinned and recent options"
              className="rounded p-1 text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground"
            >
              <EllipsisHorizontalIcon className="size-4" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem
              disabled={recent.length === 0}
              onSelect={() => void links.clearRecent().catch(() => undefined)}
            >
              Clear recent
            </DropdownMenuItem>
            <DropdownMenuItem
              disabled={pinned.length === 0}
              onSelect={() => void links.unpinAll().catch(() => undefined)}
            >
              Unpin all
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onSelect={() => void links.setShown(false).catch(() => undefined)}
            >
              Hide this section
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      {entries.map(({ link, group }) => {
        const Icon = group === "pinned" ? StarSolidIcon : ClockIcon;
        return (
          <Link
            key={link.href}
            href={link.href}
            onClick={onNavigate}
            title={`${link.kind} · ${link.href}`}
            className="group flex h-9 min-w-0 items-center gap-3 rounded-md pl-3 pr-2 text-sm text-muted-foreground transition-colors hover:bg-sidebar-accent/60 hover:text-foreground"
          >
            <Icon
              className={cn(
                "size-4 shrink-0",
                group === "pinned" && "text-primary",
              )}
            />
            {/* min-w-0: a flex item will not shrink below its text without it. */}
            <span className="min-w-0 flex-1 truncate">{link.label}</span>
            <span className="ml-auto shrink-0 text-xs opacity-60">
              {link.kind}
            </span>
          </Link>
        );
      })}
    </div>
  );
}
