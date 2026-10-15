"use client";

import { useCallback, useRef, useState } from "react";

import { bucketClient } from "@/lib/connect/client";
import type { BucketGrant } from "@/gen/paladin/admin/v1/bucket_service_pb";
import { useBumpRefresh } from "@/context/RefreshContext";
import { errorMessage } from "@/hooks/errorContract";

/**
 * useBucketGrants — the tenants other than its owner that may bind
 * Collections to one bucket (ADR-0032), over BucketService.ListBucketGrants,
 * GrantBucket and RevokeBucketGrant.
 *
 * `bucketName` is the bucket's resource name,
 * `storageBackends/{backend_id}/buckets/{bucket_id}`.
 *
 * Follows the hooks' error contract (README.md): fetchGrants is a query and
 * reports through `error`; grant and revoke are mutations and throw. A revoke
 * the server refuses because the tenant still has collections in the bucket
 * throws a ConnectError with reason BUCKET_GRANT_IN_USE for the caller to
 * explain.
 */
export function useBucketGrants(bucketName: string) {
  const bumpRefresh = useBumpRefresh();
  const [grants, setGrants] = useState<BucketGrant[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Only the latest fetch writes the list: a refresh may start while the
  // mount fetch is still in flight, and the older answer must not win.
  const seq = useRef(0);

  const fetchGrants = useCallback(async () => {
    const mine = ++seq.current;
    setLoading(true);
    setError(null);
    try {
      const res = await bucketClient.listBucketGrants({ name: bucketName });
      if (mine === seq.current) setGrants(res.grants);
      return res.grants;
    } catch (err) {
      // Query contract (state-only): surface via `error`, never throw.
      if (mine === seq.current) {
        setError(errorMessage(err, "Failed to load the bucket's grants"));
      }
      return [];
    } finally {
      if (mine === seq.current) setLoading(false);
    }
  }, [bucketName]);

  const grantBucket = useCallback(
    async (tenantId: string): Promise<BucketGrant> => {
      // Mutation contract (throw-only): caller surfaces via errorMessage().
      const created = await bucketClient.grantBucket({
        name: bucketName,
        tenantId,
      });
      setGrants((prev) => [
        ...prev.filter((g) => g.tenantId !== tenantId),
        created,
      ]);
      bumpRefresh("buckets");
      return created;
    },
    [bucketName, bumpRefresh],
  );

  const revokeBucketGrant = useCallback(
    async (tenantId: string): Promise<void> => {
      // Mutation contract (throw-only): caller surfaces via errorMessage().
      await bucketClient.revokeBucketGrant({ name: bucketName, tenantId });
      setGrants((prev) => prev.filter((g) => g.tenantId !== tenantId));
      bumpRefresh("buckets");
    },
    [bucketName, bumpRefresh],
  );

  return {
    grants,
    loading,
    error,
    fetchGrants,
    grantBucket,
    revokeBucketGrant,
  };
}
