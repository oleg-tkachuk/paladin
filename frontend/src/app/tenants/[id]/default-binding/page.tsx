"use client";

// Default Route — ADR-0010 Phase 3. Manages the tenant's default
// (backend, bucket) binding: where a bare object-key name (created without
// naming a bucket) lands. Backed by TenantService.{Get,Set,Clear}
// TenantDefaultBinding.

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Code, ConnectError } from "@connectrpc/connect";

import { useTenant } from "../tenant-context";
import { useBuckets } from "@/hooks/useBuckets";
import { tenantClient } from "@/lib/connect/client";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { useNotification } from "@/components/ui/Notification";

export default function DefaultBindingPage() {
  const { tenantId, slug } = useTenant();
  const name = `tenants/${slug}`;
  const { showNotification } = useNotification();
  const { buckets, fetchBuckets } = useBuckets();

  const [selected, setSelected] = useState(-1); // index into buckets, -1 = none
  const [busy, setBusy] = useState(false);

  // A missing binding is a normal state, not an error — NotFound resolves to
  // null rather than throwing. Any other failure toasts and surfaces via the
  // query error.
  const bindingQuery = useQuery({
    queryKey: ["defaultBinding", tenantId],
    enabled: !!tenantId,
    retry: false,
    queryFn: async ({ signal }) => {
      try {
        return await tenantClient.getTenantDefaultBinding({ name }, { signal });
      } catch (err) {
        if (err instanceof ConnectError && err.code === Code.NotFound) {
          return null;
        }
        showNotification({
          type: "error",
          title: "Failed to load default binding",
          message:
            err instanceof ConnectError
              ? err.rawMessage
              : "Failed to load default binding",
        });
        throw err;
      }
    },
  });
  const binding = bindingQuery.data ?? null;

  useEffect(() => {
    fetchBuckets(undefined, "", "", tenantId); // tenant-owned buckets only
  }, [fetchBuckets, tenantId]);

  const handleSet = async () => {
    const b = buckets[selected];
    if (!b) return;
    setBusy(true);
    try {
      const res = await tenantClient.setTenantDefaultBinding({
        name,
        backendId: b.backendId,
        bucketName: b.bucketName,
      });
      await bindingQuery.refetch();
      showNotification({
        type: "success",
        title: "Default binding set",
        message: `${res.backendId} / ${res.bucketName}`,
      });
    } catch (e) {
      showNotification({
        type: "error",
        title: "Failed to set default binding",
        message: e instanceof ConnectError ? e.rawMessage : "",
      });
    } finally {
      setBusy(false);
    }
  };

  const handleClear = async () => {
    setBusy(true);
    try {
      await tenantClient.clearTenantDefaultBinding({ name });
      setSelected(-1);
      await bindingQuery.refetch();
      showNotification({ type: "success", title: "Default binding cleared" });
    } catch (e) {
      showNotification({
        type: "error",
        title: "Failed to clear default binding",
        message: e instanceof ConnectError ? e.rawMessage : "",
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-4">
      <Card className="space-y-4 p-5">
        <div>
          <h2 className="text-base font-semibold">Default route</h2>
          <p className="text-sm text-muted-foreground">
            Where a bare object-key name (created without naming a bucket) lands
            for this tenant. Creating an object key with no bucket and no
            default route is rejected.
          </p>
        </div>

        {bindingQuery.isLoading ? (
          <Skeleton className="h-8 w-64" />
        ) : binding ? (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <Badge>{binding.backendId}</Badge>
            <span className="text-muted-foreground">/</span>
            <Badge>{binding.bucketName}</Badge>
            {binding.setBy && (
              <span className="text-xs text-muted-foreground">
                set by {binding.setBy}
              </span>
            )}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            No default route set — bare-name creation will be rejected.
          </p>
        )}

        <div className="flex flex-wrap items-end gap-2">
          <label className="text-sm">
            <span className="mb-1 block text-muted-foreground">Bucket</span>
            <select
              className="h-9 rounded-md border border-border bg-background px-2 text-sm"
              value={selected}
              onChange={(e) => setSelected(Number(e.target.value))}
              aria-label="Default bucket"
            >
              <option value={-1}>Select a bucket…</option>
              {buckets.map((b, i) => (
                <option key={`${b.backendId}/${b.bucketName}`} value={i}>
                  {b.backendId} / {b.bucketName}
                </option>
              ))}
            </select>
          </label>
          <Button onClick={handleSet} disabled={busy || selected < 0}>
            {binding ? "Update" : "Set"}
          </Button>
          {binding && (
            <Button variant="outline" onClick={handleClear} disabled={busy}>
              Clear
            </Button>
          )}
        </div>
      </Card>
    </div>
  );
}
