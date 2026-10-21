"use client";

import { useCallback, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";

import { collectionClient } from "@/lib/connect/client";
import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import {
  CollectionAccess,
  CollectionSchema,
} from "@/gen/paladin/admin/v1/types_pb";
import { useActingTenantId } from "@/hooks/useActingTenant";
import { useBumpRefresh, type RefreshTopic } from "@/context/RefreshContext";
import { errorMessage } from "@/hooks/errorContract";
import { fieldMask, type MaskField } from "@/lib/connect/fieldMask";
import { useOptimistic } from "@/hooks/useOptimistic";
import { edit } from "@/lib/optimistic";
import {
  COLLECTIONS_PARENT_INDEX,
  COLLECTIONS_QUERY_KEY,
  collectionsQuery,
} from "@/lib/queries/collections";

// Ceiling on how many pages one exhaustive fetch will follow — a backstop, not
// a limit anyone should reach.

/**
 * useCollections — wrapper around admin/v1.CollectionService.
 *
 * Resource names:
 *   Collection → "tenants/{tenant_id}/collections/{collection}"
 *   Bucket    → "storageBackends/{backend_id}/buckets/{bucket_id}"
 *
 * The new schema replaced flat `policy`/`backendId`/`bucketId` fields
 * with a nested `collectionResource` carrying `cedarPolicy: string` and
 * `bucket: string` (full resource name). Page-level callers still use the
 * legacy positional signatures — the hook builds the new request shapes.
 *
 * `parent` is the tenant the page acts on (useActingTenantId): the routed
 * tenant under /tenants/<id>/, the signed-in user's own elsewhere.
 */

const collectionResourceName = (tenantId: string, collection: string) =>
  `tenants/${tenantId}/collections/${collection}`;

const bucketResourceName = (backendId: string, bucketId: string) =>
  `storageBackends/${backendId}/buckets/${bucketId}`;

/** How a page reads Collections. */
export interface UseCollectionsOptions {
  /**
   * Every tenant's: the platform's catalogue. The server answers an empty
   * parent so for a platform admin only; anyone else gets their own tenant's.
   * Without it, the tenant the page acts on.
   */
  catalogue?: boolean;
  /** CEL over the collections, applied on the server; "" for none. */
  filter?: string;
  /** False holds the read, as a dialog does until it opens. */
  enabled?: boolean;
}

/** The collection a list row is, by its tenant and its id. */
const isCollection =
  (tenantId: string, collection: string) => (c: Collection) =>
    c.tenantId === tenantId && c.collection === collection;

export function useCollections({
  catalogue = false,
  filter = "",
  enabled = true,
}: UseCollectionsOptions = {}) {
  const actingTenantId = useActingTenantId();
  const tenantParent =
    !catalogue && actingTenantId ? `tenants/${actingTenantId}` : "";
  const bumpRefresh = useBumpRefresh();
  const qc = useQueryClient();
  const optimistic = useOptimistic();

  const query = useQuery({
    ...collectionsQuery(tenantParent, filter),
    // A tenant page waits for its tenant; the catalogue's parent is "".
    enabled: enabled && (catalogue || Boolean(actingTenantId)),
    // While a new search loads, keep showing the last result of this scope:
    // without it every keystroke emptied the table for a round trip. Never
    // across scopes: another tenant's rows must not stand in for these.
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[COLLECTIONS_PARENT_INDEX] === tenantParent
        ? previous
        : undefined,
  });
  const collections = useMemo(() => query.data ?? [], [query.data]);
  const error = query.error
    ? errorMessage(query.error, "Failed to fetch collections")
    : null;
  const { refetch: refetchQuery } = query;
  const refetch = useCallback(() => void refetchQuery(), [refetchQuery]);

  // Every collections list reads again after a change: the server's answer
  // may differ from the prediction, and other scopes hold the same rows.
  const settle = useCallback(
    (also: RefreshTopic[] = []) => {
      void qc.invalidateQueries({ queryKey: COLLECTIONS_QUERY_KEY });
      bumpRefresh(["collections", ...also]);
    },
    [qc, bumpRefresh],
  );

  const createCollection = useCallback(
    async (
      collection: string,
      displayName: string = "",
      backendId: string = "",
      bucketId: string = "",
      cedarPolicy: string = "",
      publicRead: { cacheControl: string } | null = null,
    ): Promise<Collection> => {
      try {
        const tenantId = actingTenantId;
        const collectionResource = create(CollectionSchema, {
          name: collectionResourceName(tenantId, collection),
          tenantId,
          collection,
          displayName,
          bucket:
            backendId && bucketId
              ? bucketResourceName(backendId, bucketId)
              : "",
          cedarPolicy,
          resourceVersion: "",
          // ADR-0027: a collection's access equals its bucket's, fixed at
          // creation. An empty cacheControl takes the server's default.
          access: publicRead
            ? CollectionAccess.PUBLIC_READ
            : CollectionAccess.PRIVATE,
          cacheControl: publicRead?.cacheControl ?? "",
        });
        const created = await collectionClient.createCollection({
          parent: tenantParent,
          collection,
          collectionResource,
        });
        // The server routes it to a bucket: nothing to predict.
        settle();
        return created;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [tenantParent, actingTenantId, settle],
  );

  const deleteCollection = useCallback(
    async (
      collection: string,
      resourceVersion: string = "",
      skipVersionCheck: boolean = false,
      tenant: string = actingTenantId,
    ): Promise<void> => {
      // Mutation contract (throw-only): caller surfaces via errorMessage().
      try {
        await optimistic(
          [
            edit<Collection[]>(COLLECTIONS_QUERY_KEY, (list) =>
              list.filter((c) => !isCollection(tenant, collection)(c)),
            ),
          ],
          () =>
            collectionClient.deleteCollection({
              name: collectionResourceName(tenant, collection),
              resourceVersion,
              skipVersionCheck,
            }),
        );
      } finally {
        settle(["objects"]);
      }
    },
    [actingTenantId, optimistic, settle],
  );

  const getCollection = useCallback(
    async (collection: string): Promise<Collection | null> => {
      try {
        return await collectionClient.getCollection({
          name: collectionResourceName(actingTenantId, collection),
        });
      } catch (err) {
        // Imperative read (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [actingTenantId],
  );

  const updateCollection = useCallback(
    async (
      collection: string,
      resourceVersion: string,
      updatePaths: MaskField<typeof CollectionSchema>[],
      fields: { displayName?: string; cedarPolicy?: string } = {},
    ): Promise<Collection> => {
      const tenantId = actingTenantId;
      const collectionResource = create(CollectionSchema, {
        name: collectionResourceName(tenantId, collection),
        tenantId,
        collection,
        displayName: fields.displayName ?? "",
        cedarPolicy: fields.cedarPolicy ?? "",
        resourceVersion,
      });
      // Only the fields the mask names change; the rest stay as they are.
      const changed = Object.fromEntries(
        updatePaths.map((path) => [path, collectionResource[path]]),
      );
      try {
        return await optimistic(
          [
            edit<Collection[]>(COLLECTIONS_QUERY_KEY, (list) =>
              list.map((c) =>
                isCollection(tenantId, collection)(c)
                  ? { ...c, ...changed }
                  : c,
              ),
            ),
          ],
          () =>
            collectionClient.updateCollection({
              name: collectionResourceName(tenantId, collection),
              resourceVersion,
              updateMask: fieldMask(CollectionSchema, ...updatePaths),
              collectionResource,
            }),
        );
      } finally {
        settle();
      }
    },
    [actingTenantId, optimistic, settle],
  );

  return {
    collections,
    loading: query.isFetching,
    error,
    refetch,
    createCollection,
    deleteCollection,
    getCollection,
    updateCollection,
  };
}
