/**
 * Where a console page belongs (ADR-0033): which space it is in, and which
 * sidebar section owns it. Both are decided here, from the path alone, so the
 * sidebar's highlight, the scope badge and the breadcrumb trail cannot tell a
 * page's place differently.
 */

/** The platform's sections, each the path its sidebar row links to. */
export const SECTION = {
  dashboard: "/",
  upload: "/upload",
  storageBackends: "/storage-backends",
  buckets: "/buckets",
  collections: "/collections",
  tenants: "/tenants",
  users: "/users",
  policies: "/policies",
  trash: "/trash",
  billing: "/billing",
  stats: "/stats",
  audit: "/audit",
  mcp: "/mcp",
  health: "/health",
  profile: "/profile",
  config: "/config",
} as const;

export type Section = (typeof SECTION)[keyof typeof SECTION];

const TENANTS_ROOT = "tenants";
const BACKENDS_ROOT = "storage-backends";
const BUCKETS_SEGMENT = "buckets";

/** The space a page is in: the platform's, or one tenant's workspace. */
export type Scope =
  { kind: "platform" } | { kind: "tenant"; tenant: string; tab: string };

function segmentsOf(pathname: string): string[] {
  return pathname.split("/").filter(Boolean).map(decodeURIComponent);
}

/**
 * The page's space. Everything under /tenants/{t}/ is that tenant's
 * workspace — `tab` is the first segment after the tenant, "" on its
 * overview; the tenants index itself is the platform's catalogue.
 */
export function scopeFor(pathname: string): Scope {
  const [root, tenant, tab = ""] = segmentsOf(pathname);
  if (root === TENANTS_ROOT && tenant) {
    return { kind: "tenant", tenant, tab };
  }
  return { kind: "platform" };
}

/**
 * The sidebar section that owns a page. A bucket lives under its backend's
 * URL but is a Buckets page; a backend is a Storage Backends page; a tenant's
 * workspace belongs to Tenants. Anything else is owned by the section its
 * first segment names, or by none.
 */
export function sectionFor(pathname: string): Section | null {
  const segments = segmentsOf(pathname);
  if (segments.length === 0) return SECTION.dashboard;
  const [root, , child] = segments;
  if (root === BACKENDS_ROOT && child === BUCKETS_SEGMENT) {
    return SECTION.buckets;
  }
  const section = `/${root}`;
  const known = Object.values(SECTION) as string[];
  return known.includes(section) ? (section as Section) : null;
}
