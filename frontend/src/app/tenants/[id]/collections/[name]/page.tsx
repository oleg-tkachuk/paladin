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

import { IconTone } from "@/components/ui/IconChip";
import { CopyableId } from "@/components/ui/CopyableId";
import { HelpTip } from "@/components/ui/HelpTip";
import { useTenantChangesBlocked } from "../../tenant-context";
import { PublicReadBadge } from "@/components/features/buckets/PublicReadBadge";
import { useCallback, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { LinkIcon, IdentificationIcon } from "@heroicons/react/24/outline";

import { Card, CardTitle, CardHeader, CardContent } from "@/components/ui/Card";
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
import { Timestamp } from "@/components/Timestamp";
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
  const changesBlocked = useTenantChangesBlocked();
  const { collection, setCollection } = useCollection();
  const { showNotification } = useNotification();
  // Only the buckets this collection's tenant may bind to (ADR-0032): one it
  // neither owns nor was granted is refused on Bind, so it is not offered.
  const {
    buckets,
    error: bucketsError,
    refetch: fetchBuckets,
  } = useBuckets({ accessibleTo: `tenants/${collection.tenantId}` });

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
      <Card>
        <CardHeader>
          <CardTitle
            className="text-sm font-semibold"
            icon={IdentificationIcon}
          >
            Identity
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <dl className="grid grid-cols-[140px_1fr] gap-x-3 gap-y-2 text-sm">
            <dt className="text-muted-foreground">Resource name</dt>
            <dd>
              <CopyableId value={collection.name} label="resource name" />
            </dd>

            <dt className="text-muted-foreground">Tenant ID</dt>
            <dd>
              <CopyableId value={collection.tenantId} label="tenant ID" />
            </dd>

            <dt className="text-muted-foreground">Collection</dt>
            <dd className="font-mono text-xs">{collection.collection}</dd>

            <dt className="text-muted-foreground">Completion mode</dt>
            <dd>
              <Badge variant="outline" className={T.code}>
                {completionModeLabel(collection.completionMode)}
              </Badge>
            </dd>

            <dt className="text-muted-foreground">Access</dt>
            <dd className="flex items-center gap-2">
              {isPublic ? (
                <PublicReadBadge />
              ) : (
                <span className="text-xs text-muted-foreground">private</span>
              )}
              <HelpTip topic="publicRead" />
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
              <Timestamp ts={collection.createdAt} />
            </dd>

            <dt className="text-muted-foreground">Updated</dt>
            <dd className="font-mono text-xs">
              <Timestamp ts={collection.updatedAt} />
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
                disabled={
                  Boolean(changesBlocked) ||
                  savingMeta ||
                  displayName === collection.displayName
                }
                title={changesBlocked ?? undefined}
              >
                {savingMeta ? "Saving…" : "Save"}
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* ─── Bucket binding ──────────────────────────────────────────── */}
      <Card>
        <CardHeader className="flex items-center gap-2">
          <CardTitle
            className="text-sm font-semibold"
            icon={LinkIcon}
            tone={IconTone.Info}
          >
            Bucket binding
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
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
                Boolean(changesBlocked) ||
                isPublic ||
                binding ||
                !bucketSelection ||
                bucketSelection === collection.bucket
              }
              title={changesBlocked ?? undefined}
            >
              {binding ? "Binding…" : "Re-bind"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
