"use client";

// Default Route — ADR-0014 Phase 3. Manages the tenant's default
// (backend, bucket) binding: where a bare collection name (created without
// naming a bucket) lands. Backed by TenantService.{Get,Set,Clear}
// TenantDefaultBinding.

import { useCallback, useEffect, useState } from "react";
import {
  bucketResourceName,
  parseBucketResourceName,
} from "@/lib/resources/bucket-name";
import { useQuery } from "@tanstack/react-query";
import { Code, ConnectError } from "@connectrpc/connect";

import { useTenant } from "../tenant-context";
import { useBuckets } from "@/hooks/useBuckets";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { tenantClient } from "@/lib/connect/client";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { useNotification } from "@/components/ui/Notification";
import { isAbortError, errorMessage } from "@/hooks/errorContract";

/** Renders a bucket reference as backend / bucket, falling back to the raw
 *  resource name when it does not parse. */
function BucketBadges({ bucket }: { bucket: string }) {
  const ref = parseBucketResourceName(bucket);
  if (!ref) return <Badge>{bucket}</Badge>;
  return (
    <>
      <Badge>{ref.backendId}</Badge>
      <span className="text-muted-foreground">/</span>
      <Badge>{ref.bucketId}</Badge>
    </>
  );
}

export default function DefaultBindingPage() {
  const { tenantId, slug } = useTenant();
  const name = `tenants/${slug}`;
  const { showNotification } = useNotification();
  const { buckets, error: bucketsError, fetchBuckets } = useBuckets();

  const [selected, setSelected] = useState(-1); // index into buckets, -1 = none
  const [busy, setBusy] = useState(false);
  // Clearing the route makes every bare-name create in this tenant fail from
  // that moment — a client that names no bucket stops working. It was one
  // click on an outline button beside Update.
  const [confirmClear, setConfirmClear] = useState(false);

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
        // An aborted query is not a failure the operator needs to see:
        // TanStack cancels in-flight reads on unmount and on supersede.
        if (isAbortError(err)) throw err;
        if (err instanceof ConnectError && err.code === Code.NotFound) {
          return null;
        }
        showNotification({
          type: "error",
          title: "Failed to load default binding",
          message: errorMessage(err, "Failed to load default binding"),
        });
        throw err;
      }
    },
  });
  const binding = bindingQuery.data ?? null;

  // Tenant-owned buckets only. One place, so Retry asks for the same list.
  const loadBuckets = useCallback(
    () => fetchBuckets(undefined, "", "", tenantId),
    [fetchBuckets, tenantId],
  );
  useEffect(() => {
    void loadBuckets();
  }, [loadBuckets]);

  const handleSet = async () => {
    const b = buckets[selected];
    if (!b) return;
    setBusy(true);
    try {
      const res = await tenantClient.setTenantDefaultBinding({
        name,
        bucket: bucketResourceName(b.backendId, b.bucketId),
      });
      await bindingQuery.refetch();
      showNotification({
        type: "success",
        title: "Default binding set",
        message: res.bucket,
      });
    } catch (e) {
      showNotification({
        type: "error",
        title: "Failed to set default binding",
        message: errorMessage(e, ""),
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
        message: errorMessage(e, ""),
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
            Where a bare collection name (created without naming a bucket) lands
            for this tenant. Creating an collection with no bucket and no
            default route is rejected.
          </p>
        </div>

        {bindingQuery.isLoading ? (
          <Skeleton className="h-8 w-64" />
        ) : binding ? (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <BucketBadges bucket={binding.bucket} />
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

        {/* A failed read left the select holding only "Select a bucket…" and
            Set disabled, which read as a tenant that owns no buckets. */}
        {bucketsError ? (
          <ListLoadError
            variant="inline"
            what="Buckets"
            reason={bucketsError}
            onRetry={() => void loadBuckets()}
          />
        ) : null}
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
                <option key={`${b.backendId}/${b.bucketId}`} value={i}>
                  {b.backendId} / {b.bucketId}
                </option>
              ))}
            </select>
          </label>
          <Button onClick={handleSet} disabled={busy || selected < 0}>
            {binding ? "Update" : "Set"}
          </Button>
          {binding && (
            <Button
              variant="outline"
              onClick={() => setConfirmClear(true)}
              disabled={busy}
            >
              Clear
            </Button>
          )}
        </div>
      </Card>
      <ConfirmModal
        isOpen={confirmClear}
        onClose={() => setConfirmClear(false)}
        onConfirm={handleClear}
        type="danger"
        title="Clear the default route?"
        message="Creating a Collection or object without naming a bucket will be refused in this tenant until a route is set again."
        confirmText="Clear route"
        loading={busy}
      />
    </div>
  );
}
