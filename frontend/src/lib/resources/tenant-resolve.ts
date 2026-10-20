"use client";

// Tenant identifier resolution. Routes use slug-first URLs
// (`/tenants/<slug>/...`), but operators copy-pasting from CLI /
// audit logs land with a UUID. The route handler accepts either —
// when the input is a UUID, the page replaceState's the URL with
// the canonical slug form so the address bar always reads as the
// human-friendly identifier.
//
// Slug-rename caveat: a tenant whose slug got rotated (via the
// admin RenameTenantSlug RPC) breaks any bookmark using the old
// slug — that URL 404s as designed (fail-fast over silent redirect
// to the wrong tenant). A future "slug rename history redirect"
// follow-up could keep old slugs alive for a grace window using
// the audit_log; tracked in BACKLOG.

import { useRouter } from "next/navigation";
import { useEffect } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery } from "@tanstack/react-query";

import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { tenantDetailQuery } from "@/lib/queries/details";

/**
 * Resolved tenant view used by route handlers. Tenant ID is always
 * the canonical UUID; slug is always present (backend constructs it
 * from the slug column or from the UUID prefix).
 */
export type ResolvedTenant = {
  tenantId: string;
  slug: string;
  displayName: string;
  // "shared" (default) | "dedicated" — ADR-0015 physical bucket layout.
  storageLayout: string;
  /** In the trash: the tenant is frozen and takes no changes. */
  trashed: boolean;
};

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isUuid(s: string): boolean {
  return UUID_RE.test(s);
}

/**
 * useTenantResolve fetches the tenant by `id` (slug OR UUID), and
 * when the URL identifier is a UUID and the canonical slug differs
 * from it, replaceState's the URL to the slug form. Returns
 * { tenant, loading, error, errorIsNotFound, retry } with a stable
 * shape.
 *
 * The route stays the route — we only swap the `[id]` segment, not
 * the rest of the path, so deep URLs like
 * `/tenants/<uuid>/buckets/X/Y/lifecycle` canonicalise to
 * `/tenants/<slug>/buckets/X/Y/lifecycle` in one history-replace.
 *
 * Error semantics: `errorIsNotFound` is set on Connect's NOT_FOUND
 * code so callers can distinguish "wrong URL" (= 404 page) from
 * "transient network/auth blip" (= retry banner). Without this
 * the layer above 404'd on every flake — see the original symptom
 * "page sometimes shows data, sometimes blank on refresh".
 */
// canonicalPath returns the current address with the tenant UUID `id`
// replaced by `slug`, or null when the address is already canonical.
function canonicalPath(id: string, slug: string): string | null {
  if (!isUuid(id) || slug === id || typeof window === "undefined") {
    return null;
  }
  const url = new URL(window.location.href);
  const parts = url.pathname.split("/");
  const idx = parts.indexOf("tenants");
  if (idx < 0 || parts[idx + 1] !== id) {
    return null;
  }
  parts[idx + 1] = encodeURIComponent(slug);
  url.pathname = parts.join("/");
  return url.pathname + url.search + url.hash;
}

/** The resource-name prefix GetTenant answers with. */
const TENANT_NAME_PREFIX = "tenants/";
/** The physical layout a tenant has when it names none (ADR-0015). */
const DEFAULT_STORAGE_LAYOUT = "shared";

/** resolvedTenant is the view of `res` route handlers read, `id` as asked. */
export function resolvedTenant(res: Tenant, id: string): ResolvedTenant {
  const name = res.name ?? "";
  const tenantId = name.startsWith(TENANT_NAME_PREFIX)
    ? name.slice(TENANT_NAME_PREFIX.length)
    : res.tenantId || id;
  // The slug column is NOT NULL (001_initial_schema.sql); the UUID stands in
  // so the breadcrumb renders rather than going blank if one comes back empty.
  const slug = res.slug || tenantId;
  return {
    tenantId,
    slug,
    displayName: res.displayName || slug,
    storageLayout: res.storageLayout || DEFAULT_STORAGE_LAYOUT,
    trashed: res.deletedAt !== undefined,
  };
}

/**
 * Reads through tenantDetailQuery, the query a tenants row warms on hover and
 * a tenant hover card answers from, so the page opens on what is cached.
 */
export function useTenantResolve(id: string): {
  tenant: ResolvedTenant | null;
  loading: boolean;
  error: Error | null;
  errorIsNotFound: boolean;
  retry: () => void;
} {
  const router = useRouter();
  const query = useQuery({
    ...tenantDetailQuery(id),
    select: (res) => resolvedTenant(res, id),
  });
  const tenant = query.data ?? null;
  // Auto-canonicalise: the caller landed via a UUID and the slug differs. Stay
  // loading while it lands: a different value in the [id] segment is a
  // different route subtree to the app router, so the replace remounts this
  // layout and every page under it, and a page rendered first would lose an
  // open dialog or a policy being typed. The e2e suite caught it against a
  // cluster, where the replace lands mid-test.
  const canonical = tenant ? canonicalPath(id, tenant.slug) : null;
  useEffect(() => {
    if (canonical) router.replace(canonical);
  }, [canonical, router]);

  const err = query.error;
  return {
    tenant: canonical ? null : tenant,
    loading: query.isPending || canonical !== null,
    error: err ? (err instanceof Error ? err : new Error(String(err))) : null,
    errorIsNotFound: err instanceof ConnectError && err.code === Code.NotFound,
    retry: () => void query.refetch(),
  };
}
