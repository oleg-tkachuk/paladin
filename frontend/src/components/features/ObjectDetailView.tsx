"use client";

import { DEFAULT_OBJECT_KEY } from "@/constants";
import React from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import Image from "next/image";
import { PageHeader } from "@/components/layout/PageHeader";
import { useObject } from "@/hooks/useObject";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import {
  DocumentIcon,
  ClipboardIcon,
  ArrowLeftIcon,
  ArrowDownTrayIcon,
  PencilSquareIcon,
  TrashIcon,
  PlusIcon,
  XMarkIcon,
  CheckIcon,
  EllipsisHorizontalIcon,
  ShareIcon,
  ArrowPathIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";
import { cn, formatBytes, copyToClipboard } from "@/lib/utils";
import { useNotification } from "@/components/ui/Notification";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { Dropdown } from "@/components/ui/Dropdown";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/Skeleton";
import { Separator } from "@/components/ui/separator";
import { IdentifierCopy } from "@/components/ui/IdentifierCopy";
import { T } from "@/lib/ui/typography";

// SpecRow renders one fact in the Specs sidebar — label above value,
// value gets full sidebar width to wrap into. Children are usually
// `<span>value</span>` and optionally a copy button; the row's flex
// layout keeps the value and the trailing action on the same baseline
// while letting the value `break-all` wrap when it's a long mono
// string (UUID, path).
function SpecRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-0.5">
      <dt className={T.label}>{label}</dt>
      <dd className="flex min-w-0 items-start gap-2">{children}</dd>
    </div>
  );
}

interface ObjectDetailViewProps {
  objectKey: string;
  parentObjectKey?: string;
}

function formatExpiresAt(seconds: bigint | undefined): string {
  if (!seconds) return "Never";
  try {
    return new Date(Number(seconds) * 1000).toLocaleString();
  } catch {
    return "—";
  }
}

function stateColorClasses(state: ObjectState): {
  text: string;
  dot: string;
} {
  if (state === ObjectState.AVAILABLE)
    return { text: "text-chart-2", dot: "bg-chart-2" };
  if (state === ObjectState.DELETED)
    return { text: "text-destructive", dot: "bg-destructive" };
  return { text: "text-chart-3", dot: "bg-chart-3" };
}

export function ObjectDetailView({
  objectKey,
  parentObjectKey = DEFAULT_OBJECT_KEY,
}: ObjectDetailViewProps) {
  const router = useRouter();
  const { showNotification } = useNotification();
  const {
    object,
    downloadUrl,
    loading,
    patchObjectMeta,
    softDeleteObject,
    restoreObject,
    purgeObject,
  } = useObject(objectKey, parentObjectKey);

  // State for tag editing
  const [editingLabels, setEditingLabels] = React.useState<
    Record<string, string>
  >({});
  const [isEditing, setIsEditing] = React.useState(false);
  const [newLabelKey, setNewLabelKey] = React.useState("");
  const [newLabelValue, setNewLabelValue] = React.useState("");
  const [isSaving, setIsSaving] = React.useState(false);

  // Confirmation modal state
  const [confirmAction, setConfirmAction] = React.useState<
    "trash" | "purge" | "restore" | null
  >(null);
  const [isConfirming, setIsConfirming] = React.useState(false);

  React.useEffect(() => {
    if (object?.tags) {
      setEditingLabels({ ...object.tags });
    }
  }, [object]);

  const handleSaveLabels = async () => {
    try {
      setIsSaving(true);
      await patchObjectMeta(editingLabels);
      setIsEditing(false);
    } catch {
      // Error handled by hook
    } finally {
      setIsSaving(false);
    }
  };

  const removeLabel = (key: string) => {
    const newLabels = { ...editingLabels };
    delete newLabels[key];
    setEditingLabels(newLabels);
  };

  const addLabel = () => {
    if (!newLabelKey || !newLabelValue) return;
    setEditingLabels({ ...editingLabels, [newLabelKey]: newLabelValue });
    setNewLabelKey("");
    setNewLabelValue("");
  };

  const handleCopy = async (text: string, label: string) => {
    const ok = await copyToClipboard(text);
    if (ok) {
      showNotification({
        type: "success",
        title: "Copied",
        message: `${label} copied to clipboard.`,
      });
    }
  };

  const handleShare = () => {
    if (typeof window !== "undefined") {
      handleCopy(window.location.href, "Page link");
    }
  };

  const handleDownload = () => {
    if (downloadUrl) {
      window.open(downloadUrl.url, "_blank");
    } else {
      showNotification({
        type: "warning",
        title: "Download Unavailable",
        message: "This object is not in a state that allows downloading.",
      });
    }
  };

  const handleConfirmAction = async () => {
    setIsConfirming(true);
    try {
      if (confirmAction === "trash") {
        await softDeleteObject();
        router.push("/objects");
      } else if (confirmAction === "purge") {
        await purgeObject();
        router.push("/objects");
      } else if (confirmAction === "restore") {
        await restoreObject();
      }
    } catch {
      // Error handled by hook
    } finally {
      setIsConfirming(false);
      setConfirmAction(null);
    }
  };

  // ─── Loading ─────────────────────────────────────────────────────────
  if (loading) {
    return (
      <div className="space-y-6">
        <PageHeader
          title={
            <h1 className="flex items-center gap-3 truncate text-2xl font-semibold tracking-tight">
              <Skeleton className="size-8" />
              <Skeleton className="h-6 w-64" />
            </h1>
          }
          showDefaultActions={false}
        />
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <div className="space-y-4 lg:col-span-2">
            <Skeleton className="h-[360px] w-full" />
            <Skeleton className="h-40 w-full" />
            <Skeleton className="h-32 w-full" />
          </div>
          <Skeleton className="h-64 w-full" />
        </div>
      </div>
    );
  }

  // ─── Missing ─────────────────────────────────────────────────────────
  if (!object) {
    return (
      <div className="space-y-6">
        <PageHeader
          title={
            <h1 className="flex items-center gap-3 truncate text-2xl font-semibold tracking-tight">
              <Button
                variant="ghost"
                size="icon"
                onClick={() => router.push("/objects")}
                aria-label="Back to Objects"
              >
                <ArrowLeftIcon className="size-5" />
              </Button>
              <span>Object Not Found</span>
            </h1>
          }
          showDefaultActions={false}
        />
        <Card className="flex flex-col items-center gap-3 p-12 text-center">
          <ExclamationTriangleIcon className="size-10 text-destructive" />
          <div className="text-sm font-medium">Missing Object</div>
          <div className="max-w-md text-sm text-muted-foreground">
            The object you&apos;re looking for doesn&apos;t exist or you
            don&apos;t have access.
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => router.push("/objects")}
          >
            Back to Objects
          </Button>
        </Card>
      </div>
    );
  }

  const fileName = object.key.split("/").pop() || object.key;
  const isImage = object.contentType?.startsWith("image/");
  const stateColors = stateColorClasses(object.state);

  return (
    <div className="space-y-6">
      <PageHeader
        showDefaultActions={false}
        showBreadcrumbs
        title={
          <h1 className="flex items-center gap-3 truncate text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
            <Button
              variant="ghost"
              size="icon"
              onClick={() => router.push("/objects")}
              aria-label="Back to Objects"
            >
              <ArrowLeftIcon className="size-5" />
            </Button>
            <DocumentIcon className="size-6 text-primary" />
            <span className="truncate font-mono text-xl">{fileName}</span>
          </h1>
        }
        description={object.key}
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setIsEditing(true)}
            >
              <PencilSquareIcon className="size-4" />
              <span className="hidden sm:inline">Edit Metadata</span>
            </Button>
            <Button variant="outline" size="sm" onClick={handleShare}>
              <ShareIcon className="size-4" />
              <span className="hidden sm:inline">Share</span>
            </Button>
            <Button size="sm" onClick={handleDownload}>
              <ArrowDownTrayIcon className="size-4" />
              Download
            </Button>
            <Dropdown align="right" width="w-56">
              <Dropdown.Trigger>
                <Button variant="outline" size="icon-sm" aria-label="More">
                  <EllipsisHorizontalIcon className="size-4" />
                </Button>
              </Dropdown.Trigger>
              <Dropdown.Menu className="py-1">
                {object.state === ObjectState.DELETED ? (
                  <Dropdown.Item onClick={() => setConfirmAction("restore")}>
                    <span className="flex items-center gap-2">
                      <ArrowPathIcon className="size-4" />
                      Restore Object
                    </span>
                  </Dropdown.Item>
                ) : (
                  <Dropdown.Item onClick={() => setConfirmAction("trash")}>
                    <span className="flex items-center gap-2">
                      <TrashIcon className="size-4" />
                      Move to Trash
                    </span>
                  </Dropdown.Item>
                )}
                <Dropdown.Item onClick={() => setConfirmAction("purge")}>
                  <span className="flex items-center gap-2 text-destructive">
                    <XMarkIcon className="size-4" />
                    Permanently Delete
                  </span>
                </Dropdown.Item>
              </Dropdown.Menu>
            </Dropdown>
          </div>
        }
      />

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        {/* ─── Main column ────────────────────────────────────────── */}
        <div className="space-y-4 lg:col-span-2">
          {/* Preview */}
          <Card className="overflow-hidden p-0">
            <div className="flex min-h-[360px] items-center justify-center bg-muted">
              {isImage && downloadUrl ? (
                <Image
                  src={downloadUrl.url}
                  alt={fileName}
                  width={800}
                  height={600}
                  className="max-h-[450px] w-auto rounded-md object-contain"
                  loading="eager"
                  unoptimized
                />
              ) : (
                <div className="flex flex-col items-center gap-2 text-muted-foreground">
                  <DocumentIcon className="size-12" />
                  <p className={T.hint}>
                    {object.contentType || "Binary object"}
                  </p>
                </div>
              )}
            </div>
          </Card>

          {/* Tags */}
          <Card className="space-y-3 p-4">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Tags</h2>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setIsEditing(!isEditing);
                  if (!isEditing) setEditingLabels({ ...object.tags });
                }}
              >
                {isEditing ? "Cancel" : "Edit"}
              </Button>
            </div>

            {isEditing ? (
              <div className="space-y-3">
                <div className="grid grid-cols-[1fr_1fr_auto] gap-2">
                  <Input
                    placeholder="TAG_KEY"
                    value={newLabelKey}
                    onChange={(e) =>
                      setNewLabelKey(e.target.value.toUpperCase())
                    }
                    className="font-mono text-xs"
                  />
                  <Input
                    placeholder="TAG_VALUE"
                    value={newLabelValue}
                    onChange={(e) => setNewLabelValue(e.target.value)}
                    className="font-mono text-xs"
                  />
                  <Button
                    size="sm"
                    onClick={addLabel}
                    disabled={!newLabelKey || !newLabelValue}
                  >
                    <PlusIcon className="size-4" />
                    Add
                  </Button>
                </div>

                {/* Free-form tags only. The reserved `object_tag` key
                    is the classification slug — surfaced in the Specs
                    sidebar instead, edited via the admin taxonomy
                    flow. Hiding it here also prevents the user from
                    accidentally clearing it via the trash button on
                    a row whose semantics don't match the rest. */}
                <div className="space-y-1.5">
                  {Object.entries(editingLabels).filter(
                    ([k]) => k !== "object_tag",
                  ).length === 0 ? (
                    <p className={T.hint}>No tags assigned.</p>
                  ) : (
                    Object.entries(editingLabels)
                      .filter(([k]) => k !== "object_tag")
                      .map(([key, value]) => (
                        <div
                          key={key}
                          className="flex items-center gap-2 rounded-md border border-border bg-muted/40 p-2"
                        >
                          <span className="font-mono text-xs text-muted-foreground">
                            {key}
                          </span>
                          <span className="text-muted-foreground">=</span>
                          <span className="flex-1 break-all font-mono text-xs">
                            {value}
                          </span>
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            onClick={() => removeLabel(key)}
                            aria-label="Remove tag"
                          >
                            <TrashIcon className="size-3.5" />
                          </Button>
                        </div>
                      ))
                  )}
                </div>

                <div className="flex justify-end">
                  <Button
                    size="sm"
                    onClick={handleSaveLabels}
                    disabled={isSaving}
                  >
                    <CheckIcon className="size-4" />
                    {isSaving ? "Saving…" : "Save"}
                  </Button>
                </div>
              </div>
            ) : (
              (() => {
                // View-mode also filters `object_tag` — its value is the
                // classification slug rendered in the Specs sidebar.
                // Computed once so the empty-state branch keys on the
                // same filtered list as the rendered one.
                const freeFormTags = Object.entries(object.tags || {}).filter(
                  ([k]) => k !== "object_tag",
                );
                return freeFormTags.length > 0 ? (
                  <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
                    {freeFormTags.map(([key, value]) => (
                      <Badge
                        key={key}
                        variant="outline"
                        className="justify-start font-mono text-xs"
                      >
                        {key}={value}
                      </Badge>
                    ))}
                  </div>
                ) : (
                  <p className={T.hint}>No tags assigned.</p>
                );
              })()
            )}
          </Card>
        </div>

        {/* ─── Sidebar ─────────────────────────────────────────────
            Single facts card. Was previously split into "Identifiers"
            (main column) + "Specs" (sidebar) but the two cards
            duplicated three rows: Object Key appeared in both
            (once as "Object Key" plain mono, once as "Storage
            ObjectKey" link), Storage Path lived only in Identifiers,
            Object UUID lived only in Identifiers. Folded them all
            into the sidebar in canonical order — identifiers first
            (UUID, path, key-link), then physical attributes (size,
            type, state, tag), then lifecycle (expires, external
            ref). Picks the more useful presentation per duplicate:
            object.objectKey renders as a link to /object-keys/<n>
            rather than plain mono. Drops the plain "Object Key" row
            and the standalone Identifiers card. ──────────────── */}
        <aside className="space-y-4">
          {/* Stacked-row layout (label above value) instead of the
              side-by-side `[140px_1fr]` dl that worked in the wide
              object-keys/[name] page but cramped the sidebar here.
              Sidebar takes 1/3 of the page width on lg+, so a 36-char
              UUID or a long Storage Path needs the full column to
              wrap into. Stacked rows give each value full width and
              `break-all` lets long mono strings wrap mid-token without
              spilling out of the card.
              A Separator between the identity group (UUID, path,
              key-link) and the attributes group (size, type, state,
              tag, expires, ref) keeps the card scannable in two
              passes — one for "what is this thing" and one for "what
              are its properties". */}
          <Card className="space-y-3 p-4">
            <h2 className="text-sm font-semibold">Specs</h2>
            <dl className="space-y-3">
              <SpecRow label="Object UUID">
                <span className="break-all font-mono text-xs">
                  {object.objectId || objectKey}
                </span>
                <IdentifierCopy
                  value={object.objectId || objectKey}
                  label="Object UUID"
                  iconOnly
                />
              </SpecRow>

              <SpecRow label="Storage Path">
                <span
                  className="break-all font-mono text-xs"
                  title={object.key}
                >
                  {object.key}
                </span>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => handleCopy(object.key, "Storage path")}
                  aria-label="Copy storage path"
                >
                  <ClipboardIcon className="size-3.5" />
                </Button>
              </SpecRow>

              <SpecRow label="Object Key">
                <Link
                  href={`/object-keys/${object.objectKey}`}
                  className="break-all font-mono text-xs text-primary hover:underline"
                >
                  {object.objectKey}
                </Link>
              </SpecRow>

              <Separator />

              <SpecRow label="Size">
                <span className="font-mono text-xs">
                  {formatBytes(object.sizeBytes)}
                </span>
              </SpecRow>

              <SpecRow label="Type">
                <span className="break-all font-mono text-xs">
                  {object.contentType || "—"}
                </span>
              </SpecRow>

              <SpecRow label="State">
                <span className={cn(T.pill, stateColors.text)}>
                  <span className={cn(T.pillDot, stateColors.dot)} />
                  {ObjectState[object.state]}
                </span>
              </SpecRow>

              {/* Classification = the reserved `object_tag` key inside
                  the same `tags` map the Tags card edits. It points at
                  a tenant-scoped taxonomy slug (admin's ObjectTag
                  resource) and is one-of, not bag-of, so it lives here
                  in Specs rather than in the Tags card. The Tags card
                  filters this key out so the same value never renders
                  twice on the page. */}
              <SpecRow label="Classification">
                {object.tags?.object_tag ? (
                  <span className="font-mono text-xs">
                    {object.tags.object_tag}
                  </span>
                ) : (
                  <span className="font-mono text-xs text-muted-foreground">
                    —
                  </span>
                )}
              </SpecRow>

              <SpecRow label="Expires">
                <span className="font-mono text-xs">
                  {formatExpiresAt(object.presignExpiresAt?.seconds)}
                </span>
              </SpecRow>

              <SpecRow label="External Ref">
                <span className="break-all font-mono text-xs">
                  {object.externalRef || "—"}
                </span>
              </SpecRow>
            </dl>
          </Card>
        </aside>
      </div>

      {/* Confirmation Modals */}
      <ConfirmModal
        isOpen={confirmAction === "trash"}
        onClose={() => setConfirmAction(null)}
        onConfirm={handleConfirmAction}
        title="Move to Trash"
        message="This object will be soft-deleted and moved to the trash bin. You can restore it later."
        type="warning"
        confirmText="Move to Trash"
        loading={isConfirming}
      />
      <ConfirmModal
        isOpen={confirmAction === "purge"}
        onClose={() => setConfirmAction(null)}
        onConfirm={handleConfirmAction}
        title="Permanently Delete"
        message="This action is irreversible. The object and all its data will be permanently destroyed."
        type="danger"
        confirmText="Delete Forever"
        loading={isConfirming}
      />
      <ConfirmModal
        isOpen={confirmAction === "restore"}
        onClose={() => setConfirmAction(null)}
        onConfirm={handleConfirmAction}
        title="Restore Object"
        message="This object will be recovered from the trash and set back to available status."
        type="warning"
        confirmText="Restore"
        loading={isConfirming}
      />
    </div>
  );
}
