// The recent and pinned resources: what a visit adds, what a pin toggles,
// and which pages count. Pure, so the shell and its tests agree.
import { crumbs, type SegmentNames } from "@/components/layout/crumbs";

import { ResourceLinksSchema, type ResourceLink } from "./keys";

/** How many recently opened resources are kept. */
export const RECENT_MAX = 8;
/** How many resources may be pinned. */
export const PINNED_MAX = 12;

/** The links a stored value holds, or none when it is not a list of links. */
export function readLinks(value: unknown): ResourceLink[] {
  const parsed = ResourceLinksSchema.safeParse(value);
  return parsed.success ? parsed.data : [];
}

/**
 * The resource a page shows, when it shows one: its path ends in an id
 * (a tenant, a collection, a user), not a route name like "objects".
 */
export function resourceOf(
  pathname: string,
  names: SegmentNames = {},
): ResourceLink | null {
  const trail = crumbs(pathname, names);
  const last = trail.at(-1);
  const parent = trail.at(-2);
  if (!last?.mono || !parent) return null;
  return { href: pathname, label: last.label, kind: singular(parent.label) };
}

function singular(label: string): string {
  return label.endsWith("s") ? label.slice(0, -1) : label;
}

/** The recent list after a visit: it first, once, at most RECENT_MAX. */
export function withVisit(
  recent: readonly ResourceLink[],
  visit: ResourceLink,
): ResourceLink[] {
  return [visit, ...recent.filter((r) => r.href !== visit.href)].slice(
    0,
    RECENT_MAX,
  );
}

/** The pinned list with `link` pinned or, if it was, unpinned. */
export function togglePinned(
  pinned: readonly ResourceLink[],
  link: ResourceLink,
): ResourceLink[] {
  if (pinned.some((p) => p.href === link.href)) {
    return pinned.filter((p) => p.href !== link.href);
  }
  return [...pinned, link].slice(-PINNED_MAX);
}

/** Two lists the same, so a visit that changes nothing writes nothing. */
export function sameLinks(
  a: readonly ResourceLink[],
  b: readonly ResourceLink[],
): boolean {
  return (
    a.length === b.length &&
    a.every((l, i) => l.href === b[i].href && l.label === b[i].label)
  );
}

/** Where a link to a resource is listed. */
export type LinkGroup = "pinned" | "recent";

/**
 * The pinned resources, then the recent ones not already pinned: one entry
 * per page, in the order the sidebar and the palette show them.
 */
export function groupedLinks(
  pinned: readonly ResourceLink[],
  recent: readonly ResourceLink[],
): { link: ResourceLink; group: LinkGroup }[] {
  const pinnedHrefs = new Set(pinned.map((p) => p.href));
  return [
    ...pinned.map((link) => ({ link, group: "pinned" as const })),
    ...recent
      .filter((r) => !pinnedHrefs.has(r.href))
      .map((link) => ({ link, group: "recent" as const })),
  ];
}
