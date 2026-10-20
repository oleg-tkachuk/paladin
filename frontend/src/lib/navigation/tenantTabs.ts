/**
 * A tenant workspace's tabs (ADR-0033), grouped by what an operator comes
 * for. A group with several pages shows them as a second row; each page keeps
 * its own URL under /tenants/{t}/, so links and bookmarks survive the
 * grouping.
 */

/** One page of a workspace: its URL segment ("" for the overview). */
export interface TenantPage {
  slug: string;
  label: string;
}

export interface TenantTabGroup {
  label: string;
  pages: readonly TenantPage[];
}

export const TENANT_TAB_GROUPS: readonly TenantTabGroup[] = [
  { label: "Overview", pages: [{ slug: "", label: "Overview" }] },
  {
    label: "Collections",
    pages: [{ slug: "collections", label: "Collections" }],
  },
  {
    label: "Storage",
    pages: [
      { slug: "buckets", label: "Buckets" },
      { slug: "default-binding", label: "Default bucket" },
    ],
  },
  {
    label: "Access",
    pages: [
      { slug: "policies", label: "Policies" },
      { slug: "capabilities", label: "Capabilities" },
      { slug: "m2m-tokens", label: "M2M tokens" },
    ],
  },
  {
    label: "Activity",
    // History first: what changed, who changed it and how it ended, read
    // before the raw audit log or the event deliveries behind it.
    pages: [
      { slug: "history", label: "History" },
      { slug: "event-subscriptions", label: "Events" },
      { slug: "audit-log", label: "Audit" },
    ],
  },
  {
    label: "Limits",
    pages: [
      { slug: "quotas", label: "Quotas" },
      { slug: "budget", label: "Budget" },
    ],
  },
];

/** The group, and the page in it, a workspace tab names. */
export interface TenantTabState {
  group: TenantTabGroup;
  page: TenantPage;
}

/**
 * The tab state for `tab`, the first path segment after the tenant ("" on
 * its overview). A segment no page names falls to the overview, so a new
 * route is never shown under the wrong group.
 */
export function tenantTabState(tab: string): TenantTabState {
  for (const group of TENANT_TAB_GROUPS) {
    const page = group.pages.find((p) => p.slug === tab);
    if (page) return { group, page };
  }
  const [overview] = TENANT_TAB_GROUPS;
  return { group: overview, page: overview.pages[0] };
}

/** The URL of a workspace page. */
export function tenantPageHref(base: string, page: TenantPage): string {
  return page.slug ? `${base}/${page.slug}` : base;
}
