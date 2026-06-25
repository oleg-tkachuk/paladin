"use client";

import { DEFAULT_OBJECT_KEY } from "@/constants";
import React from "react";
import { useRouter, usePathname } from "next/navigation";
import Image from "next/image";
import { PageHeader } from "@/components/layout/PageHeader";
import { useObject } from "@/hooks/useObject";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import {
  DocumentIcon,
  ArrowLeftIcon,
  ArrowDownTrayIcon,
  PencilSquareIcon,
  TrashIcon,
  XMarkIcon,
  EllipsisHorizontalIcon,
  ShareIcon,
  ArrowPathIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";
import { copyToClipboard } from "@/lib/utils";
import { useNotification } from "@/components/ui/Notification";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { Dropdown } from "@/components/ui/Dropdown";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/Skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ObjectVersionsTab } from "@/components/features/ObjectVersionsTab";
import { ObjectTagsCard } from "@/components/features/ObjectTagsCard";
import { ObjectSpecsPanel } from "@/components/features/ObjectSpecsPanel";
import { T } from "@/lib/ui/typography";

interface ObjectDetailViewProps {
  objectKey: string;
  parentObjectKey?: string;
}

export function ObjectDetailView({
  objectKey,
  parentObjectKey = DEFAULT_OBJECT_KEY,
}: ObjectDetailViewProps) {
  const router = useRouter();
  const pathname = usePathname();
  const { showNotification } = useNotification();

  // Back-link target. The ObjectDetailView is now mounted under
  // /tenants/<id>/object-keys/<name>/objects/<objectId>; "back" should
  // land on the Objects tab one level up. Derive from the current
  // pathname rather than threading a prop through (the parent route is
  // implicit in the URL — propagating it would just duplicate it).
  // Falls back to /tenants if the pathname doesn't fit the expected
  // shape, which only happens if the component is mounted outside the
  // OK subtree (no current callsite, but safe default).
  const backHref = (() => {
    const m = pathname?.match(
      /^(\/tenants\/[^/]+\/object-keys\/[^/]+\/objects)(\/|$)/,
    );
    return m ? m[1] : "/tenants";
  })();
  const {
    object,
    downloadUrl,
    loading,
    refresh,
    patchObjectMeta,
    softDeleteObject,
    restoreObject,
    purgeObject,
  } = useObject(objectKey, parentObjectKey);

  // Active tab (Object | Versions). Persisted in URL hash so a deep link
  // from the audit log (`#versions`) lands on the right view, and so the
  // browser back button preserves tab context. Read once on mount via
  // `window.location.hash` (SSR-safe), update via `history.replaceState`
  // on user-driven changes.
  const [activeTab, setActiveTab] = React.useState<"object" | "versions">(
    "object",
  );
  React.useEffect(() => {
    if (typeof window === "undefined") return;
    const hash = window.location.hash.replace(/^#/, "");
    if (hash === "versions" || hash === "object") {
      setActiveTab(hash);
    }
  }, []);
  const handleTabChange = (next: string) => {
    if (next !== "object" && next !== "versions") return;
    setActiveTab(next);
    if (typeof window !== "undefined") {
      const url = `${window.location.pathname}${window.location.search}#${next}`;
      window.history.replaceState(null, "", url);
    }
  };

  // The Tags card owns its edit draft; the parent keeps only the isEditing
  // flag so both the header "Edit Metadata" button and the card's own "Edit"
  // button can toggle it.
  const [isEditing, setIsEditing] = React.useState(false);

  // Confirmation modal state
  const [confirmAction, setConfirmAction] = React.useState<
    "trash" | "purge" | "restore" | null
  >(null);
  const [isConfirming, setIsConfirming] = React.useState(false);

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
        router.push(backHref);
      } else if (confirmAction === "purge") {
        await purgeObject();
        router.push(backHref);
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
      <div
        className="space-y-6"
        role="status"
        aria-busy="true"
        aria-label="Loading object details"
      >
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
                onClick={() => router.push(backHref)}
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
            onClick={() => router.push(backHref)}
          >
            Back to Objects
          </Button>
        </Card>
      </div>
    );
  }

  const fileName = object.key.split("/").pop() || object.key;
  const isImage = object.contentType?.startsWith("image/");

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
              onClick={() => router.push(backHref)}
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
                <Button
                  variant="outline"
                  size="icon-sm"
                  aria-label="Object actions"
                >
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
        {/* ─── Main column ──────────────────────────────────────────
            Wrapped in a Tabs container so the Versions history view
            can swap in without disturbing the Specs sidebar (which
            always reflects the *current* version regardless of tab).
            Active tab is mirrored into the URL hash so audit-log
            deep links can land on `#versions`. */}
        <div className="space-y-3 lg:col-span-2">
          <Tabs
            value={activeTab}
            onValueChange={handleTabChange}
            className="gap-3"
          >
            <TabsList className="w-full overflow-x-auto sm:w-fit">
              <TabsTrigger value="object">Object</TabsTrigger>
              <TabsTrigger value="versions">Versions</TabsTrigger>
            </TabsList>

            <TabsContent value="object" className="space-y-4">
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

              <ObjectTagsCard
                tags={object.tags}
                isEditing={isEditing}
                onEditToggle={setIsEditing}
                onSave={patchObjectMeta}
              />
            </TabsContent>

            <TabsContent value="versions">
              <ObjectVersionsTab
                active={activeTab === "versions"}
                object={object}
                onObjectChanged={refresh}
              />
            </TabsContent>
          </Tabs>
        </div>

        <ObjectSpecsPanel
          object={object}
          objectKey={objectKey}
          onCopy={handleCopy}
        />
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
