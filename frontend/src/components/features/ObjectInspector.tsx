"use client";

import { useObject } from "@/hooks/useObject";
import { useScope } from "@/context/ScopeContext";
import { useTenantOptional } from "@/app/tenants/[id]/tenant-context";
import { IdentifierCopy } from "@/components/ui/IdentifierCopy";
import { useRouter } from "next/navigation";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import {
  XMarkIcon,
  TrashIcon,
  ArrowPathIcon,
  DocumentIcon,
  EyeIcon,
} from "@heroicons/react/24/outline";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn, formatBytes } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { formatDateTime } from "@/lib/format/locale";

interface ObjectInspectorProps {
  collection: string | null;
  parentCollection?: string;
  onClose: () => void;
}

export function ObjectInspector({
  collection,
  parentCollection,
  onClose,
}: ObjectInspectorProps) {
  const router = useRouter();
  // Fall back to the user's currently-selected Collection scope when the
  // caller doesn't pass one explicitly. The /objects page browses inside
  // a single scope at a time, so this keeps the lookup honest — without
  // this, the Inspector always queries `collection: "default"` and any
  // object outside the bootstrap default returns `not_found`.
  const { collection: scopedCollection, tenantId, tenant } = useScope();
  // The page's own tenant wins over the scope picker, as it does for the
  // object lookup: an operator browsing another tenant's page keeps the
  // picker on their own.
  const routedTenant = useTenantOptional();
  const effectiveParent = parentCollection || scopedCollection;
  const { object, downloadUrl, loading, softDeleteObject, restoreObject } =
    useObject(collection || undefined, effectiveParent);

  if (!collection) return null;

  const isImage = object?.contentType?.startsWith("image/");

  return (
    <>
      {/* Backdrop for mobile */}
      <div
        className="fixed inset-0 z-40 bg-background/60 backdrop-blur-sm transition-opacity lg:hidden"
        onClick={onClose}
      />

      {/* Inspector Panel */}
      <div
        className={cn(
          "fixed top-0 right-0 z-[60] flex h-full w-full flex-col overflow-hidden border-l border-border bg-card text-card-foreground shadow-lg sm:w-125",
          "animate-slide-in-right",
        )}
      >
        {/* Header */}
        <div className="shrink-0 space-y-3 border-b border-border p-4">
          <div className="flex items-start justify-between gap-2">
            <div className="flex min-w-0 items-center gap-2">
              <DocumentIcon className="size-5 shrink-0 text-primary" />
              <div className="min-w-0">
                <h2 className="text-sm font-semibold">Quick Inspect</h2>
                {object && (
                  <p
                    className={cn(
                      T.codeSmall,
                      "truncate text-muted-foreground",
                    )}
                    title={object.key}
                  >
                    {object.key}
                  </p>
                )}
              </div>
            </div>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={onClose}
              aria-label="Close"
            >
              <XMarkIcon className="size-4" />
            </Button>
          </div>

          {object && (
            <div className="flex flex-wrap items-center gap-2">
              <span className={cn(T.pill, "text-chart-2")}>
                <span className={cn(T.pillDot, "bg-chart-2")} />
                {ObjectState[object.state]}
              </span>
              <Badge variant="outline" className={T.codeSmall}>
                {formatBytes(object.sizeBytes)}
              </Badge>
            </div>
          )}
        </div>

        {/* Content */}
        <div className="flex-1 space-y-4 overflow-y-auto p-4">
          {loading ? (
            <div className="space-y-3">
              <Skeleton className="aspect-video w-full" />
              <Skeleton className="h-32 w-full" />
              <Skeleton className="h-20 w-full" />
            </div>
          ) : object ? (
            <>
              {/* Preview */}
              <div className="flex aspect-video items-center justify-center overflow-hidden rounded-md border border-border bg-muted">
                {isImage && downloadUrl ? (
                  // eslint-disable-next-line @next/next/no-img-element
                  <img
                    src={downloadUrl.url}
                    alt="Preview"
                    className="h-full w-full object-contain"
                  />
                ) : (
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <DocumentIcon className="size-10" />
                    <span className={T.hint}>
                      {object.contentType || "binary"}
                    </span>
                  </div>
                )}
              </div>

              {/* Specs */}
              <div className="space-y-3">
                <h3 className="text-sm font-semibold">Specs</h3>
                <dl className="grid grid-cols-[120px_1fr] gap-x-3 gap-y-2 text-sm">
                  <dt className="text-muted-foreground">Storage Key</dt>
                  <dd
                    className="break-all font-mono text-xs"
                    title={object.key}
                  >
                    {object.key}
                  </dd>

                  <dt className="text-muted-foreground">Object ID</dt>
                  <dd>
                    <IdentifierCopy
                      value={object.objectId}
                      label="Object ID"
                      iconOnly
                    />
                    <span className="sr-only">{object.objectId}</span>
                  </dd>

                  <dt className="text-muted-foreground">MIME Type</dt>
                  <dd className="font-mono text-xs">
                    {object.contentType || "—"}
                  </dd>

                  <dt className="text-muted-foreground">Classification</dt>
                  <dd className="font-mono text-xs">
                    {object.tags?.object_tag || "—"}
                  </dd>

                  <dt className="text-muted-foreground">Expiration</dt>
                  <dd className="font-mono text-xs">
                    {object.presignExpiresAt?.seconds
                      ? formatDateTime(
                          new Date(
                            Number(object.presignExpiresAt.seconds) * 1000,
                          ),
                        )
                      : "Never"}
                  </dd>
                </dl>
              </div>

              {/* Free-form tags only — `object_tag` is the
                  Classification slug shown in Specs above and is
                  edited via the admin taxonomy flow, not here. */}
              {(() => {
                const freeFormTags = Object.entries(object.tags || {}).filter(
                  ([k]) => k !== "object_tag",
                );
                return (
                  <div className="space-y-2">
                    <h3 className="text-sm font-semibold">Tags</h3>
                    {freeFormTags.length > 0 ? (
                      <div className="flex flex-wrap gap-1.5">
                        {freeFormTags.map(([k, v]) => (
                          <Badge
                            key={k}
                            variant="outline"
                            className="font-mono text-xs"
                          >
                            {k}={String(v)}
                          </Badge>
                        ))}
                      </div>
                    ) : (
                      <p className={T.hint}>No tags assigned.</p>
                    )}
                  </div>
                );
              })()}
            </>
          ) : (
            <p className="py-12 text-center text-sm text-muted-foreground">
              Object unavailable.
            </p>
          )}
        </div>

        {/* Actions Footer */}
        {object && (
          <div className="shrink-0 border-t border-border bg-card p-3">
            {object.state === ObjectState.DELETED ? (
              <Button
                variant="success"
                size="sm"
                className="w-full"
                onClick={() => restoreObject()}
              >
                <ArrowPathIcon className="size-4" />
                Restore Object
              </Button>
            ) : (
              <div className="grid grid-cols-2 gap-2">
                <Button
                  size="sm"
                  onClick={() => {
                    // Object detail moved under the tenant subtree
                    // in Phase 5: /tenants/<id>/collections/<collection>/
                    // objects/<key>. Prefer slug; fall back to UUID
                    // (resolver canonicalises on landing).
                    const handle =
                      routedTenant?.slug || tenant?.slug || tenantId || "";
                    if (!handle) return;
                    router.push(
                      `/tenants/${encodeURIComponent(handle)}/collections/${encodeURIComponent(object.collection)}/objects/${encodeURIComponent(object.key)}`,
                    );
                  }}
                >
                  <EyeIcon className="size-4" />
                  Full Details
                </Button>
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => {
                    softDeleteObject();
                    onClose();
                  }}
                >
                  <TrashIcon className="size-4" />
                  Trash
                </Button>
              </div>
            )}
          </div>
        )}
      </div>
    </>
  );
}
