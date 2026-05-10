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
import { useEffect, useState } from "react";

import { tenantClient } from "@/lib/connect/client";

/**
 * Resolved tenant view used by route handlers. Tenant ID is always
 * the canonical UUID; slug is always present (backend constructs it
 * from the slug column or from the UUID prefix).
 */
export type ResolvedTenant = {
  tenantId: string;
  slug: string;
  displayName: string;
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
 * { tenant, loading, error } with a stable shape.
 *
 * The route stays the route — we only swap the `[id]` segment, not
 * the rest of the path, so deep URLs like
 * `/tenants/<uuid>/buckets/X/Y/lifecycle` canonicalise to
 * `/tenants/<slug>/buckets/X/Y/lifecycle` in one history-replace.
 */
export function useTenantResolve(id: string): {
  tenant: ResolvedTenant | null;
  loading: boolean;
  error: Error | null;
} {
  const router = useRouter();
  const [state, setState] = useState<{
    tenant: ResolvedTenant | null;
    loading: boolean;
    error: Error | null;
  }>({ tenant: null, loading: true, error: null });

  useEffect(() => {
    let cancelled = false;
    // Synchronously reset to loading on id change so the layout
    // skeleton renders immediately while the new tenant is fetched.
    // The eslint rule flags this as set-state-in-effect, but the
    // alternative (deriving loading from a ref or via a reducer) is
    // boilerplate-heavy for the same observable behaviour. The reset
    // is gated on `id` so it doesn't loop.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setState({ tenant: null, loading: true, error: null });
    tenantClient
      .getTenant({ name: `tenants/${id}` })
      .then((res) => {
        if (cancelled) return;
        // The proto Tenant type carries `name = tenants/{tenant_id}`
        // (server canonicalises to UUID) and a slug we render in URLs.
        // Some legacy responses may omit slug; fall back to the UUID
        // segment from `name`.
        const name = res.name ?? "";
        const tenantId = name.startsWith("tenants/")
          ? name.slice("tenants/".length)
          : (res.tenantId ?? id);
        // Tenant proto carries `slug` (UNIQUE kebab-case handle,
        // populated by the server from `tenants.slug`). Empty
        // shouldn't happen after migration-009 backfilled every
        // row, but fall back to the UUID so the breadcrumb still
        // renders rather than going blank if it does.
        const slug = res.slug || tenantId;
        const tenant: ResolvedTenant = {
          tenantId,
          slug,
          displayName: res.displayName || slug,
        };
        setState({ tenant, loading: false, error: null });

        // Auto-canonicalise: caller landed via UUID but slug differs.
        // window.location is the only reliable source of the FULL
        // current path here (router params don't expose it cleanly
        // from a layout). Replace just the `[id]` segment, keep the
        // rest verbatim — including query string and hash.
        if (isUuid(id) && tenant.slug !== id && typeof window !== "undefined") {
          const url = new URL(window.location.href);
          // Path shape: /tenants/<id>/<...rest>
          const parts = url.pathname.split("/");
          const idx = parts.indexOf("tenants");
          if (idx >= 0 && parts[idx + 1] === id) {
            parts[idx + 1] = encodeURIComponent(tenant.slug);
            url.pathname = parts.join("/");
            router.replace(url.pathname + url.search + url.hash);
          }
        }
      })
      .catch((err) => {
        if (cancelled) return;
        setState({
          tenant: null,
          loading: false,
          error: err instanceof Error ? err : new Error(String(err)),
        });
      });
    return () => {
      cancelled = true;
    };
  }, [id, router]);

  return state;
}
