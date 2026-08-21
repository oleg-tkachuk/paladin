"use client";

import { DEFAULT_OBJECT_KEY } from "@/constants";
import React from "react";
import { useRouter, usePathname } from "next/navigation";
import Image from "next/image";
import { PageHeader } from "@/components/layout/PageHeader";
import { useObject } from "@/hooks/useObject";
import { DocumentIcon, ArrowLeftIcon } from "@heroicons/react/24/outline";
import { copyToClipboard } from "@/lib/utils";
import { useNotification } from "@/components/ui/Notification";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ObjectVersionsTab } from "@/components/features/ObjectVersionsTab";
import { ObjectDetailActions } from "@/components/features/ObjectDetailActions";
import {
  ObjectDetailSkeleton,
  ObjectNotFound,
} from "@/components/features/ObjectDetailStates";
import { ObjectTagsCard } from "@/components/features/ObjectTagsCard";
import { ObjectLockCard } from "@/components/features/ObjectLockCard";
import { ObjectSpecsPanel } from "@/components/features/ObjectSpecsPanel";
import { T } from "@/lib/ui/typography";

interface ObjectDetailViewProps {
  collection: string;
  parentCollection?: string;
}

export function ObjectDetailView({
  collection,
  parentCollection = DEFAULT_OBJECT_KEY,
}: ObjectDetailViewProps) {
  const router = useRouter();
  const pathname = usePathname();
  const { showNotification } = useNotification();

  // Back-link target. The ObjectDetailView is now mounted under
  // /tenants/<id>/collections/<name>/objects/<objectId>; "back" should
  // land on the Objects tab one level up. Derive from the current
  // pathname rather than threading a prop through (the parent route is
  // implicit in the URL — propagating it would just duplicate it).
  // Falls back to /tenants if the pathname doesn't fit the expected
  // shape, which only happens if the component is mounted outside the
  // OK subtree (no current callsite, but safe default).
  const backHref = (() => {
    const m = pathname?.match(
      /^(\/tenants\/[^/]+\/collections\/[^/]+\/objects)(\/|$)/,
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
  } = useObject(collection, parentCollection);

  // Active tab (Object | Versions). Persisted in URL hash so a deep link
  // from the audit log (`#versions`) lands on the right view, and so the
  // browser back button preserves tab context. Read once on mount via
  // `window.location.hash` (SSR-safe), update via `history.replaceState`
  // on user-driven changes.
  const [activeTab, setActiveTab] = React.useState<"object" | "versions">(
    "object",
  );
  // Sync the tab from the URL hash AFTER hydration. This is the legitimate
  // "sync with a browser-only source" exception to set-state-in-effect:
  // reading window.location.hash during render (lazy init) would diverge from
  // the server's "object" default and trip a hydration mismatch, so the read
  // must happen in an effect post-hydration.
  React.useEffect(() => {
    if (typeof window === "undefined") return;
    const hash = window.location.hash.replace(/^#/, "");
    if (hash === "versions" || hash === "object") {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- post-hydration browser-hash read; see comment above
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
    return <ObjectDetailSkeleton />;
  }

  // ─── Missing ─────────────────────────────────────────────────────────
  if (!object) {
    return <ObjectNotFound onBack={() => router.push(backHref)} />;
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
          <ObjectDetailActions
            state={object.state}
            onEdit={() => setIsEditing(true)}
            onShare={handleShare}
            onDownload={handleDownload}
            onAction={setConfirmAction}
          />
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

              {/* Object Lock sits below tags rather than in the Specs panel:
                  it is an action surface, not a read-only fact, and putting a
                  COMPLIANCE control in a column of metadata invites clicking
                  it the way one edits a label. */}
              <ObjectLockCard objectName={object.name} />
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
          collection={collection}
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
