"use client";

import { useEffect, useState } from "react";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { Code, ConnectError } from "@connectrpc/connect";

import { tenantClient } from "@/lib/connect/client";

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
  const [target, setTarget] = useState<string | null>(null);

  useEffect(() => {
    const match = /^\/tenants\/([^/]+)(\/.*)?$/.exec(pathname ?? "");
    if (!match) {
      setTarget(null);
      return;
    }
    const oldSlug = decodeURIComponent(match[1]);
    const rest = match[2] ?? "";
    let cancelled = false;
    tenantClient
      .resolveRenamedSlug({ oldSlug })
      .then((res) => {
        if (cancelled || !res.newSlug) return;
        setTarget(`/tenants/${res.newSlug}${rest}`);
      })
      .catch((e: unknown) => {
        // NOT_FOUND = no rename on record / not authorized — stay silent.
        if (!(e instanceof ConnectError) || e.code !== Code.NotFound) {
          console.debug("resolveRenamedSlug failed", e);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [pathname]);

  if (!target) return null;

  return (
    <div className="mt-6 max-w-md rounded-2xl border border-indigo-500/30 bg-indigo-500/5 px-6 py-4 text-center">
      <p className="mb-3 text-sm text-slate-300">
        This workspace was renamed — its address changed.
      </p>
      <Link
        href={target}
        className="inline-block rounded-xl bg-indigo-600 px-5 py-2.5 text-sm font-bold text-white transition-all hover:bg-indigo-500 active:scale-95"
      >
        Go to the new address
      </Link>
    </div>
  );
}
