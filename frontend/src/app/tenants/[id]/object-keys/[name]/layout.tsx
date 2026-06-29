"use client";

// ObjectKey detail layout — applies to every route under
// /tenants/<id>/object-keys/<name>/. Mirrors BucketDetailLayout:
// fetch once via GetObjectKey, expose through context, render a
// tab strip; tab pages just read context.
//
// Cross-tenancy guard: ObjectKey resource_name encodes the
// tenant. If the URL's tenant doesn't match the resource's
// tenant, bounce to the canonical path so the address bar tells
// the truth.

import { useEffect, type Dispatch, type SetStateAction } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { notFound, useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  ArrowPathIcon,
  ChevronLeftIcon,
  KeyIcon,
} from "@heroicons/react/24/outline";

import { ObjectKeyTabs } from "@/components/layout/ObjectKeyTabs";
import { Skeleton } from "@/components/ui/Skeleton";
import { Button } from "@/components/ui/button";
import { objectKeyClient } from "@/lib/connect/client";
import type { ObjectKey } from "@/gen/paladin/admin/v1/types_pb";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "../../tenant-context";
import { ObjectKeyProvider } from "./objectkey-context";

const okResourceName = (tenantId: string, objectKey: string) =>
  `tenants/${tenantId}/objectKeys/${objectKey}`;

export default function ObjectKeyDetailLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const router = useRouter();
  const tenant = useTenant();
  // useParams() (sync) avoids the `use(promise)` re-suspension that
  // flashes the layout's loading skeleton on every nested-route
  // change. See TenantLayout for the full rationale.
  const params = useParams<{ name: string }>();
  const rawName = params?.name ?? "";
  const objectKeyName = decodeURIComponent(rawName);

  const queryClient = useQueryClient();
  const okKey = ["objectKey", tenant.tenantId, objectKeyName] as const;
  const okQuery = useQuery({
    queryKey: okKey,
    retry: false, // NotFound short-circuits to notFound(); a retry won't help.
    queryFn: ({ signal }) =>
      objectKeyClient.getObjectKey(
        { name: okResourceName(tenant.tenantId, objectKeyName) },
        { signal },
      ),
  });
  const ok = okQuery.data ?? null;
  const loading = okQuery.isFetching;
  const notFoundFlag =
    okQuery.error instanceof ConnectError &&
    okQuery.error.code === Code.NotFound;
  const error =
    okQuery.error && !notFoundFlag
      ? okQuery.error instanceof ConnectError
        ? okQuery.error.rawMessage
        : "Failed to load object key."
      : null;
  const refetch = async () => {
    await okQuery.refetch();
  };
  // setObjectKey for child tabs (push a server-updated ObjectKey into cache).
  const setOk: Dispatch<SetStateAction<ObjectKey | null>> = (next) =>
    queryClient.setQueryData<ObjectKey>(okKey, (curr) => {
      const resolved = typeof next === "function" ? next(curr ?? null) : next;
      return resolved ?? undefined;
    });

  // Cross-ownership guard. ObjectKey.tenantId is the canonical
  // owner — if a paste linked the wrong tenant slug, bounce. Hooks
  // before notFound() to keep the count stable.
  useEffect(() => {
    if (!ok) return;
    if (ok.tenantId && ok.tenantId !== tenant.tenantId) {
      router.replace(
        `/tenants/${ok.tenantId}/object-keys/${encodeURIComponent(
          objectKeyName,
        )}`,
      );
    }
  }, [ok, tenant.tenantId, objectKeyName, router]);

  if (notFoundFlag) {
    notFound();
  }

  return (
    // No second PageHeader here — TenantLayout already owns the
    // page chrome (breadcrumbs + separator + tab strip). Inside
    // that we render a focused OK header band: back-link + title +
    // refresh action, no separator. Keeps the visual hierarchy
    // unambiguous (one PageHeader per page).
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="space-y-1 min-w-0">
          <Link
            href={`/tenants/${tenant.slug}/object-keys`}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          >
            <ChevronLeftIcon className="size-4" />
            All Object Keys
          </Link>
          <h2 className="flex items-center gap-2 truncate text-xl font-semibold tracking-tight text-foreground sm:text-2xl">
            <KeyIcon className="size-5 text-primary" />
            <span className="truncate font-mono">{objectKeyName}</span>
          </h2>
          {ok?.displayName && (
            <p className={cn(T.helper, "truncate")}>{ok.displayName}</p>
          )}
        </div>
        <Button
          variant="outline"
          size="icon"
          onClick={() => void refetch()}
          aria-label="Refresh"
          disabled={loading}
          className="shrink-0"
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>

      <ObjectKeyTabs tenantId={tenant.slug} objectKey={objectKeyName} />

      {error && !ok ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">
          {error}
        </div>
      ) : loading && !ok ? (
        <div className="space-y-3">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : ok ? (
        <ObjectKeyProvider
          value={{ objectKey: ok, setObjectKey: setOk, refetch }}
        >
          {children}
        </ObjectKeyProvider>
      ) : null}
    </div>
  );
}
