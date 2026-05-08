"use client";

import { useObject } from "@/hooks/useObject";
import { useScope } from "@/context/ScopeContext";
import { IdentifierCopy } from "@/components/ui/IdentifierCopy";
import { ObjectTagBadge } from "@/components/features/ObjectTagBadge";
import { useRouter } from "next/navigation";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import {
  XMarkIcon,
  ArrowTopRightOnSquareIcon,
  TrashIcon,
  ArrowPathIcon,
  DocumentIcon,
  EyeIcon,
  TagIcon,
  ServerStackIcon,
} from "@heroicons/react/24/outline";
import { cn, formatBytes } from "@/lib/utils";

interface ObjectInspectorProps {
  objectKey: string | null;
  parentObjectKey?: string;
  onClose: () => void;
}

export function ObjectInspector({
  objectKey,
  parentObjectKey,
  onClose,
}: ObjectInspectorProps) {
  const router = useRouter();
  // Fall back to the user's currently-selected ObjectKey scope when the
  // caller doesn't pass one explicitly. The /objects page browses inside
  // a single scope at a time, so this keeps the lookup honest — without
  // this, the Inspector always queries `objectKey: "default"` and any
  // object outside the bootstrap default returns `not_found`.
  const { objectKey: scopedObjectKey } = useScope();
  const effectiveParent = parentObjectKey || scopedObjectKey;
  const { object, downloadUrl, loading, softDeleteObject, restoreObject } =
    useObject(objectKey || undefined, effectiveParent);

  if (!objectKey) return null;

  return (
    <>
      {/* Backdrop for mobile */}
      <div
        className={cn(
          "fixed inset-0 bg-black/40 backdrop-blur-sm z-40 lg:hidden transition-opacity",
          objectKey ? "opacity-100" : "opacity-0 pointer-events-none",
        )}
        onClick={onClose}
      />

      {/* Inspector Panel */}
      <div
        className={cn(
          "fixed top-0 right-0 w-full sm:w-[500px] h-full bg-card/95 text-card-foreground backdrop-blur-3xl border-l border-border z-[60] shadow-[-20px_0_50px_rgba(0,0,0,0.5)] transition-transform duration-500 ease-out flex flex-col overflow-hidden animate-slide-in-right",
          !objectKey && "translate-x-full",
        )}
      >
        {/* Header */}
        <div className="p-8 border-b border-border space-y-6 relative overflow-hidden shrink-0">
          <div className="absolute top-0 right-0 w-64 h-64 bg-primary/10 blur-[80px] pointer-events-none -z-10" />
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-4">
              <div className="w-12 h-12 rounded-xl bg-primary/10 border border-primary/20 flex items-center justify-center text-primary shadow-[0_0_20px_rgba(99,102,241,0.15)]">
                <DocumentIcon className="w-6 h-6" />
              </div>
              <div>
                <h2 className="text-xl font-bold text-foreground tracking-tight">
                  Quick Inspect
                </h2>
                <p className="text-xs text-primary/80 font-semibold tracking-wider uppercase mt-0.5">
                  Object Metadata
                </p>
              </div>
            </div>
            <button
              onClick={onClose}
              className="p-2.5 rounded-xl bg-accent border border-border text-muted-foreground hover:text-foreground hover:bg-accent/80 transition-all active:scale-95"
            >
              <XMarkIcon className="w-5 h-5" />
            </button>
          </div>

          {object && (
            <div className="space-y-4">
              <div className="flex flex-col gap-1">
                <div
                  className="font-mono text-sm text-foreground truncate max-w-[400px]"
                  title={object.key}
                >
                  {object.key}
                </div>
              </div>

              <div className="flex items-center gap-3">
                <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-primary/10 border border-primary/20 shadow-inner">
                  <span className="w-2 h-2 rounded-full bg-primary shadow-[0_0_8px_rgba(129,140,248,0.8)] animate-pulse" />
                  <span className="text-xs font-semibold text-primary uppercase tracking-wide">
                    {ObjectState[object.state]}
                  </span>
                </div>
                <div className="px-3 py-1.5 rounded-lg bg-accent border border-border">
                  <span className="text-xs font-medium text-foreground tracking-wide">
                    {formatBytes(object.sizeBytes)}
                  </span>
                </div>
              </div>
            </div>
          )}
        </div>

        {/* Content Area */}
        <div className="flex-1 overflow-y-auto custom-scrollbar p-8 space-y-8">
          {loading ? (
            <div className="h-full flex flex-col items-center justify-center space-y-4 opacity-70">
              <ArrowPathIcon className="w-8 h-8 text-primary animate-spin" />
              <span className="text-xs font-semibold text-muted-foreground tracking-wider uppercase">
                Loading object data...
              </span>
            </div>
          ) : object ? (
            <div className="space-y-8 animate-fade-in">
              {/* Preview Box */}
              <div className="group relative aspect-video rounded-2xl bg-background border border-border flex flex-col items-center justify-center overflow-hidden transition-all hover:border-primary/30 hover:shadow-[0_0_30px_rgba(99,102,241,0.05)]">
                <div className="absolute inset-0 bg-gradient-to-tr from-primary/5 to-transparent opacity-0 group-hover:opacity-100 transition-opacity duration-500 pointer-events-none" />
                {object.contentType?.startsWith("image/") ? (
                  // eslint-disable-next-line @next/next/no-img-element
                  <img
                    src={downloadUrl?.url || ""}
                    alt="Preview"
                    className="w-full h-full object-contain p-4 transition-transform duration-700 group-hover:scale-105"
                  />
                ) : (
                  <div className="flex flex-col items-center opacity-80 group-hover:opacity-100 transition-opacity">
                    <DocumentIcon className="w-12 h-12 text-muted-foreground group-hover:text-primary/80 transition-colors" />
                    <span className="mt-4 text-xs font-medium text-muted-foreground tracking-wide">
                      {object.contentType}
                    </span>
                  </div>
                )}
                <button
                  onClick={() =>
                    downloadUrl && window.open(downloadUrl.url, "_blank")
                  }
                  className="absolute bottom-4 right-4 p-3 rounded-xl bg-card/90 text-foreground opacity-0 translate-y-2 group-hover:translate-y-0 group-hover:opacity-100 transition-all hover:scale-105 shadow-xl border border-border backdrop-blur-md"
                  title="Open externally"
                >
                  <ArrowTopRightOnSquareIcon className="w-4 h-4" />
                </button>
              </div>

              {/* Technical Specifications */}
              <div className="space-y-3">
                <div className="flex items-center gap-2 px-1 text-primary">
                  <ServerStackIcon className="w-4 h-4" />
                  <h3 className="text-xs font-bold uppercase tracking-wider">
                    Technical Specs
                  </h3>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="p-4 rounded-xl bg-muted/30 border border-border space-y-1 hover:bg-muted/50 transition-colors">
                    <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">
                      Storage Key
                    </span>
                    <div
                      className="text-xs font-medium text-foreground truncate"
                      title={object.key}
                    >
                      {object.key}
                    </div>
                  </div>
                  <div className="p-4 rounded-xl bg-muted/30 border border-border space-y-1 hover:bg-muted/50 transition-colors">
                    <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">
                      Object ID
                    </span>
                    <IdentifierCopy
                      value={object.objectId}
                      label=""
                      className="text-xs font-medium text-foreground p-0"
                    />
                  </div>

                  <div className="p-4 rounded-xl bg-muted/30 border border-border space-y-1 hover:bg-muted/50 transition-colors">
                    <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">
                      MIME Type
                    </span>
                    <div className="text-xs font-medium text-foreground truncate">
                      {object.contentType}
                    </div>
                  </div>
                  <div className="p-4 rounded-xl bg-muted/30 border border-border space-y-1 hover:bg-muted/50 transition-colors flex flex-col justify-center">
                    <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">
                      ObjectTag
                    </span>
                    <div>
                      <ObjectTagBadge
                        objectTag={object.tags?.object_tag || "Untagged"}
                      />
                    </div>
                  </div>

                  <div className="col-span-2 p-4 rounded-xl bg-muted/30 border border-border space-y-1 hover:bg-muted/50 transition-colors">
                    <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">
                      Expiration
                    </span>
                    <div className="text-xs font-medium text-foreground">
                      {object.presignExpiresAt?.seconds
                        ? new Date(
                            Number(object.presignExpiresAt.seconds) * 1000,
                          ).toLocaleString()
                        : "Never"}
                    </div>
                  </div>
                </div>
              </div>

              {/* Tags & Metadata */}
              <div className="space-y-3">
                <div className="flex items-center justify-between px-1">
                  <div className="flex items-center gap-2 text-indigo-400">
                    <TagIcon className="w-4 h-4" />
                    <h3 className="text-xs font-bold uppercase tracking-wider">
                      Metadata Tags
                    </h3>
                  </div>
                </div>

                <div className="p-5 rounded-xl bg-muted/30 border border-border">
                  {Object.keys(object.tags || {}).length > 0 ? (
                    <div className="flex flex-wrap gap-2">
                      {Object.entries(object.tags || {}).map(([k, v]) => (
                        <div
                          key={k}
                          className="px-3 py-1.5 rounded-lg bg-primary/10 border border-primary/20 flex items-center gap-2 transition-all hover:bg-primary/20"
                        >
                          <span className="text-xs font-semibold text-primary/80">
                            {k}
                          </span>
                          <div className="w-1 h-1 rounded-full bg-border" />
                          <span
                            className="text-xs font-medium text-foreground truncate max-w-[150px]"
                            title={String(v)}
                          >
                            {String(v)}
                          </span>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="flex items-center justify-center p-4">
                      <span className="text-xs font-medium text-muted-foreground italic">
                        No tags found for this object.
                      </span>
                    </div>
                  )}
                </div>
              </div>
            </div>
          ) : (
            <div className="h-full flex items-center justify-center">
              <div className="text-muted-foreground text-sm font-medium">
                Object unavailable
              </div>
            </div>
          )}
        </div>

        {/* Actions Footer */}
        {object && (
          <div className="p-6 border-t border-border bg-card/80 backdrop-blur-md shrink-0">
            <div className="grid grid-cols-2 gap-3">
              {object.state === ObjectState.DELETED ? (
                <button
                  onClick={() => restoreObject()}
                  className="col-span-2 py-3.5 rounded-xl bg-success text-primary-foreground text-sm font-bold hover:bg-success/90 transition-all shadow-[0_4px_14px_rgba(5,150,105,0.3)] active:scale-95 flex items-center justify-center gap-2"
                >
                  <ArrowPathIcon className="w-4 h-4" />
                  Restore Object
                </button>
              ) : (
                <>
                  <button
                    className="col-span-1 py-3.5 rounded-xl bg-primary text-primary-foreground text-sm font-bold hover:bg-primary/90 transition-all shadow-[0_4px_14px_rgba(79,70,229,0.3)] active:scale-95 flex items-center justify-center gap-2"
                    onClick={() =>
                      router.push(
                        `/objects/${encodeURIComponent(object.key)}?objectKey=${encodeURIComponent(object.objectKey)}`,
                      )
                    }
                  >
                    <EyeIcon className="w-4 h-4" />
                    Full Details
                  </button>
                  <button
                    onClick={() => {
                      softDeleteObject();
                      onClose();
                    }}
                    className="col-span-1 py-3.5 rounded-xl bg-destructive/10 border border-destructive/20 text-destructive text-sm font-bold hover:bg-destructive hover:text-primary-foreground transition-all active:scale-95 flex items-center justify-center gap-2"
                  >
                    <TrashIcon className="w-4 h-4" />
                    Trash
                  </button>
                </>
              )}
            </div>
          </div>
        )}
      </div>
    </>
  );
}
