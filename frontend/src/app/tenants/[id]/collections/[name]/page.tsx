"use client";

// Collection Overview — identity + bucket-binding editor.
//
// Cedar policy editor moved out to its own `policy/` tab so the
// Overview stays a quick-glance surface; Objects + Trash get their
// own tabs (route to filtered cross-tenant views in this slice,
// move under here in a future phase).
//
// Mutation flow follows the same pattern as the bucket Lifecycle
// tab: on success, push the returned resource into context so
// sibling tabs see fresh data without a refetch.

import { PublicReadBadge } from "@/components/features/buckets/PublicReadBadge";
import { useCallback, useEffect, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { LinkIcon } from "@heroicons/react/24/outline";

import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { useNotification } from "@/components/ui/Notification";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";

import { collectionClient } from "@/lib/connect/client";
import { useBuckets } from "@/hooks/useBuckets";
import { ListLoadError } from "@/components/ui/ListLoadError";
import {
  CollectionAccess,
  CollectionSchema,
} from "@/gen/paladin/admin/v1/types_pb";
import { CompletionMode } from "@/gen/paladin/common/v1/resource_pb";
import { T } from "@/lib/ui/typography";

import { useCollection } from "./collection-context";
import { formatTimestampUTC } from "@/lib/format/timestamp";
import { errorMessage } from "@/hooks/errorContract";
import { fieldMask } from "@/lib/connect/fieldMask";

function completionModeLabel(m: CompletionMode): string {
  switch (m) {
    case CompletionMode.IMPLICIT:
      return "implicit (S3 events)";
    case CompletionMode.EXPLICIT:
      return "explicit (CompleteObject)";
    default:
      return "unspecified";
  }
}

export default function CollectionOverviewPage() {
  const { collection, setCollection } = useCollection();
  const { showNotification } = useNotification();
  const { buckets, error: bucketsError, fetchBuckets } = useBuckets();

  const [displayName, setDisplayName] = useState(collection.displayName);
  const [savingMeta, setSavingMeta] = useState(false);

  const [bucketSelection, setBucketSelection] = useState(collection.bucket);
  const [binding, setBinding] = useState(false);

  // Re-sync editor state when the context Collection changes (another tab
  // wrote and refreshed the cache, or refetch fired). Render-phase adjust-on-
  // change (not set-state-in-effect) — `collection` identity is stable between
  // renders via TanStack structural sharing.
  const [seededCollection, setSeededCollection] = useState(collection);
  if (collection !== seededCollection) {
    setSeededCollection(collection);
    setDisplayName(collection.displayName);
    setBucketSelection(collection.bucket);
  }

  useEffect(() => {
    void fetchBuckets();
  }, [fetchBuckets]);

  const handleSaveDisplayName = useCallback(async () => {
    setSavingMeta(true);
    try {
      const collectionResource = create(CollectionSchema, {
        name: collection.name,
        tenantId: collection.tenantId,
        collection: collection.collection,
        displayName,
        bucket: collection.bucket,
        cedarPolicy: collection.cedarPolicy,
        resourceVersion: collection.resourceVersion,
      });
      const updated = await collectionClient.updateCollection({
        name: collection.name,
        resourceVersion: collection.resourceVersion,
        updateMask: fieldMask(CollectionSchema, "displayName"),
        collectionResource,
      });
      setCollection(updated);
      showNotification({
        type: "success",
        title: "Display name updated",
        message: updated.displayName || updated.collection,
      });
    } catch (err) {
      const msg = errorMessage(err, "Update failed");
      showNotification({ type: "error", title: "Update failed", message: msg });
    } finally {
      setSavingMeta(false);
    }
  }, [collection, displayName, setCollection, showNotification]);

  // ADR-0027: fixed at creation; a public one never moves.
  const isPublic = collection.access === CollectionAccess.PUBLIC_READ;

  const handleBind = useCallback(async () => {
    if (!bucketSelection || bucketSelection === collection.bucket) return;
    setBinding(true);
    try {
      const updated = await collectionClient.bindCollectionToBucket({
        name: collection.name,
        resourceVersion: collection.resourceVersion,
        bucket: bucketSelection,
      });
      setCollection(updated);
      showNotification({
        type: "success",
        title: "Bucket re-bound",
        message: bucketSelection,
      });
    } catch (err) {
      const msg = errorMessage(err, "Bind failed");
      showNotification({ type: "error", title: "Bind failed", message: msg });
    } finally {
      setBinding(false);
    }
  }, [collection, bucketSelection, setCollection, showNotification]);

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      {/* ─── Identity ────────────────────────────────────────────────── */}
      <Card className="space-y-3 p-4">
        <h2 className="text-sm font-semibold">Identity</h2>
        <dl className="grid grid-cols-[140px_1fr] gap-x-3 gap-y-2 text-sm">
          <dt className="text-muted-foreground">Resource name</dt>
          <dd className="break-all font-mono text-xs">{collection.name}</dd>

          <dt className="text-muted-foreground">Tenant ID</dt>
          <dd className="break-all font-mono text-xs">{collection.tenantId}</dd>

          <dt className="text-muted-foreground">Collection</dt>
          <dd className="font-mono text-xs">{collection.collection}</dd>

          <dt className="text-muted-foreground">Completion mode</dt>
          <dd>
            <Badge variant="outline" className={T.code}>
              {completionModeLabel(collection.completionMode)}
            </Badge>
          </dd>

          <dt className="text-muted-foreground">Access</dt>
          <dd>
            {isPublic ? (
              <PublicReadBadge />
            ) : (
              <span className="text-xs text-muted-foreground">private</span>
            )}
          </dd>

          {isPublic && (
            <>
              <dt className="text-muted-foreground">Cache-Control</dt>
              <dd className="font-mono text-xs">{collection.cacheControl}</dd>
            </>
          )}

          <dt className="text-muted-foreground">Resource version</dt>
          <dd className="font-mono text-xs">
            {collection.resourceVersion || "—"}
          </dd>

          <dt className="text-muted-foreground">Created</dt>
          <dd className="font-mono text-xs">
            {formatTimestampUTC(collection.createdAt)}
          </dd>

          <dt className="text-muted-foreground">Updated</dt>
          <dd className="font-mono text-xs">
            {formatTimestampUTC(collection.updatedAt)}
          </dd>
        </dl>

        <div className="space-y-1.5 pt-2">
          <Label htmlFor="collection-display-name">Display name</Label>
          <div className="flex gap-2">
            <Input
              id="collection-display-name"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder="Production assets"
            />
            <Button
              size="sm"
              onClick={handleSaveDisplayName}
              disabled={savingMeta || displayName === collection.displayName}
            >
              {savingMeta ? "Saving…" : "Save"}
            </Button>
          </div>
        </div>
      </Card>

      {/* ─── Bucket binding ──────────────────────────────────────────── */}
      <Card className="space-y-3 p-4">
        <div className="flex items-center gap-2">
          <LinkIcon className="size-4 text-muted-foreground" />
          <h2 className="text-sm font-semibold">Bucket binding</h2>
        </div>
        <p className="text-xs text-muted-foreground">
          {isPublic
            ? "A public Collection stays in its bucket: its objects' URLs name it."
            : "Re-binding triggers an audit entry — pick a destination bucket and confirm. Existing objects keep their physical location until migrated."}
        </p>
        <div className="space-y-1">
          <Label className="text-xs">Current</Label>
          <p className="break-all font-mono text-xs">
            {collection.bucket || (
              <span className="italic text-muted-foreground">(unbound)</span>
            )}
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="collection-bucket-binding">Bind to bucket</Label>
          {/* A failed read opened a select with nothing in it, and Bind
              could never enable — as if there were no bucket to bind to. */}
          {bucketsError ? (
            <ListLoadError
              variant="inline"
              what="Buckets"
              reason={bucketsError}
              onRetry={() => void fetchBuckets()}
            />
          ) : null}
          <SelectRoot
            value={bucketSelection}
            onValueChange={setBucketSelection}
            disabled={isPublic}
          >
            <SelectTrigger id="collection-bucket-binding" className="w-full">
              <SelectValue placeholder="Pick a bucket…" />
            </SelectTrigger>
            <SelectContent>
              {buckets.map((b) => {
                const value = `storageBackends/${b.backendId}/buckets/${b.bucketId}`;
                return (
                  <SelectItem key={value} value={value}>
                    <span className="font-mono text-xs">
                      {b.backendId}/{b.bucketId}
                    </span>
                    {b.displayName ? (
                      <span className="text-muted-foreground">
                        {" "}
                        — {b.displayName}
                      </span>
                    ) : null}
                  </SelectItem>
                );
              })}
            </SelectContent>
          </SelectRoot>
        </div>
        <div className="flex justify-end">
          <Button
            size="sm"
            onClick={handleBind}
            disabled={
              isPublic ||
              binding ||
              !bucketSelection ||
              bucketSelection === collection.bucket
            }
          >
            {binding ? "Binding…" : "Re-bind"}
          </Button>
        </div>
      </Card>
    </div>
  );
}
