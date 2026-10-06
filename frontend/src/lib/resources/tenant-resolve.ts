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
import { useCallback, useEffect, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";

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
  // "shared" (default) | "dedicated" — ADR-0015 physical bucket layout.
  storageLayout: string;
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

export function useTenantResolve(id: string): {
  tenant: ResolvedTenant | null;
  loading: boolean;
  error: Error | null;
  errorIsNotFound: boolean;
  retry: () => void;
} {
  const router = useRouter();
  const [state, setState] = useState<{
    tenant: ResolvedTenant | null;
    loading: boolean;
    error: Error | null;
    errorIsNotFound: boolean;
  }>({ tenant: null, loading: true, error: null, errorIsNotFound: false });
  // Bumped by the Retry callback to re-fire the effect without
  // changing `id` (which would also reset the URL canonicaliser).
  const [retryNonce, setRetryNonce] = useState(0);
  const retry = useCallback(() => setRetryNonce((n) => n + 1), []);
  useEffect(() => {
    let cancelled = false;
    // Synchronously reset to loading on id/retry change so the
    // skeleton renders immediately while the new tenant is fetched.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setState({
      tenant: null,
      loading: true,
      error: null,
      errorIsNotFound: false,
    });
    tenantClient
      .getTenant({ name: `tenants/${id}` })
      .then((res) => {
        if (cancelled) return;
        const name = res.name ?? "";
        const tenantId = name.startsWith("tenants/")
          ? name.slice("tenants/".length)
          : (res.tenantId ?? id);
        // Tenant proto carries `slug` (UNIQUE kebab-case handle,
        // populated by the server from `tenants.slug`). Empty
        // shouldn't happen — `001_initial_schema.sql` makes the
        // column NOT NULL — but fall back to the UUID so the breadcrumb still
        // renders rather than going blank if it does.
        const slug = res.slug || tenantId;
        const tenant: ResolvedTenant = {
          tenantId,
          slug,
          displayName: res.displayName || slug,
          storageLayout: res.storageLayout || "shared",
        };

        // Auto-canonicalise: caller landed via UUID but slug differs.
        // window.location is the only reliable source of the FULL
        // current path here (router params don't expose it cleanly
        // from a layout). Replace just the `[id]` segment, keep the
        // rest verbatim — including query string and hash.
        //
        // And stay loading while it lands. A different value in the [id]
        // segment is a different route subtree to the app router, so the
        // replace remounts this layout and every page under it. Resolving
        // first rendered the page, then threw it away: a dialog the operator
        // had opened closed, a policy being typed was cleared. The e2e suite
        // caught it against a cluster, where the replace lands mid-test.
        const canonical = canonicalPath(id, slug);
        if (canonical) {
          router.replace(canonical);
          return;
        }
        setState({
          tenant,
          loading: false,
          error: null,
          errorIsNotFound: false,
        });
      })
      .catch((err) => {
        if (cancelled) return;
        const isNotFound =
          err instanceof ConnectError && err.code === Code.NotFound;
        setState({
          tenant: null,
          loading: false,
          error: err instanceof Error ? err : new Error(String(err)),
          errorIsNotFound: isNotFound,
        });
      });
    return () => {
      cancelled = true;
    };
  }, [id, router, retryNonce]);

  return { ...state, retry };
}
