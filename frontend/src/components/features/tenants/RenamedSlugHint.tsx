"use client";

import { useQuery } from "@tanstack/react-query";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { Code, ConnectError } from "@connectrpc/connect";

import { tenantClient } from "@/lib/connect/client";
import { notFoundIsAnswer } from "@/lib/connect/expected";

/**
 * RenamedSlugHint inspects the current `/tenants/<slug>/…` path and, when that
 * slug was renamed (TenantService.ResolveRenamedSlug), renders a "this
 * workspace was renamed — go to the new address" CTA that preserves the rest of
 * the path. It renders nothing while resolving, when there is no rename on
 * record, or when the caller can't read the target tenant (all of which the
 * resolver returns as NOT_FOUND) — so the surrounding 404 page degrades to the
 * plain not-found message.
 */
export function RenamedSlugHint() {
  const pathname = usePathname();
  const match = /^\/tenants\/([^/]+)(\/.*)?$/.exec(pathname ?? "");
  const oldSlug = match ? decodeURIComponent(match[1]) : "";
  const rest = match?.[2] ?? "";

  // Resolve the (possibly renamed) slug via TanStack — keyed on oldSlug (the
  // RPC's only input); the signal cancels a superseded lookup. NotFound /
  // not-authorized resolve to null so the surrounding 404 stays plain.
  const { data: newSlug } = useQuery({
    queryKey: ["renamedSlug", oldSlug],
    enabled: !!match,
    retry: false,
    queryFn: async ({ signal }) => {
      try {
        const res = await tenantClient.resolveRenamedSlug(
          { oldSlug },
          notFoundIsAnswer({ signal }),
        );
        return res.newSlug || null;
      } catch (e: unknown) {
        if (!(e instanceof ConnectError) || e.code !== Code.NotFound) {
          console.debug("resolveRenamedSlug failed", e);
        }
        return null;
      }
    },
  });

  const target = newSlug ? `/tenants/${newSlug}${rest}` : null;
  if (!target) return null;

  return (
    <div className="mt-6 max-w-md rounded-2xl border border-primary/30 bg-primary/5 px-6 py-4 text-center">
      <p className="mb-3 text-sm text-foreground">
        This workspace was renamed — its address changed.
      </p>
      <Link
        href={target}
        className="inline-block rounded-xl bg-primary px-5 py-2.5 text-sm font-bold text-primary-foreground transition-all hover:bg-primary active:scale-95"
      >
        Go to the new address
      </Link>
    </div>
  );
}
