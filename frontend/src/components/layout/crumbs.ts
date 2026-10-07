/**
 * The route as words: one label per URL segment, shared by the breadcrumb
 * trail and the document title so the two cannot describe a page differently.
 *
 * ENTITY_PARENTS marks segments whose *child* is a resource id (slug / key /
 * UUID) rather than a route name — those children are decoded and rendered in
 * mono; everything else is title-cased.
 */
const ENTITY_PARENTS = new Set([
  "tenants",
  "users",
  "buckets",
  "collections",
  "objects",
  "object-tags",
]);

/**
 * Segments whose title-cased form is wrong: acronyms, which title case
 * turns into "Mcp" and "M2m tokens".
 */
const SEGMENT_LABELS: Record<string, string> = {
  mcp: "MCP",
  "m2m-tokens": "M2M tokens",
  oauth: "OAuth",
  login: "Sign in",
};

/** A UUID segment is an id wherever it sits, never words to title-case. */
const UUID_SEGMENT =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

// Past this, a resource id is cut and ends in an ellipsis.
const MAX_ID_CHARS = 32;

export const PRODUCT_NAME = "Paladin";
/** What "/" is called: the sidebar's name for it. */
export const HOME_LABEL = "Dashboard";
const TITLE_SEPARATOR = " · ";

export interface Crumb {
  segment: string;
  label: string;
  /** A resource id rather than a route name. */
  mono: boolean;
  /** Where the crumb leads, when not to its own path. */
  href?: string;
}

/** What a page calls the segments of its path that are ids; see
 *  CrumbNamesContext. */
export type SegmentNames = Readonly<
  Record<string, { label: string; href?: string }>
>;

function resolveLabel(segment: string, mono: boolean): string {
  if (mono) {
    const decoded = decodeURIComponent(segment);
    // Keys can be paths ("folder/sub/file.txt") — show the leaf.
    const leaf = decoded.includes("/")
      ? decoded.split("/").pop() || decoded
      : decoded;
    return leaf.length > MAX_ID_CHARS
      ? leaf.slice(0, MAX_ID_CHARS - 3) + "…"
      : leaf;
  }
  const fixed = SEGMENT_LABELS[segment];
  if (fixed) return fixed;
  return segment.charAt(0).toUpperCase() + segment.slice(1).replace(/-/g, " ");
}

export function crumbs(pathname: string, names: SegmentNames = {}): Crumb[] {
  const segments = pathname.split("/").filter(Boolean);
  return segments.map((segment, i) => {
    const named = names[segment];
    if (named)
      return { segment, label: named.label, mono: true, href: named.href };
    const mono =
      (i > 0 && ENTITY_PARENTS.has(segments[i - 1])) ||
      UUID_SEGMENT.test(segment);
    return { segment, label: resolveLabel(segment, mono), mono };
  });
}

/**
 * The document title: the trail read from the page outwards, then the
 * product. "Quotas · acme · Tenants · Paladin" — the part that tells two
 * tabs apart comes first, where a narrow tab still shows it.
 */
export function pageTitle(pathname: string, names: SegmentNames = {}): string {
  const labels = crumbs(pathname, names).map((c) => c.label);
  if (labels.length === 0) labels.push(HOME_LABEL);
  return [...labels.reverse(), PRODUCT_NAME].join(TITLE_SEPARATOR);
}
