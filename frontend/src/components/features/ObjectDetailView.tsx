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
  TagIcon,
  GlobeAltIcon,
  ShieldCheckIcon,
  PencilSquareIcon,
  TrashIcon,
  PlusIcon,
  XMarkIcon,
  CheckIcon,
  EllipsisHorizontalIcon,
  ShareIcon,
  ArrowPathIcon,
} from "@heroicons/react/24/outline";
import { ObjectTagBadge } from "@/components/features/ObjectTagBadge";
import { cn, formatBytes, copyToClipboard } from "@/lib/utils";
import { useNotification } from "@/components/ui/Notification";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { Dropdown } from "@/components/ui/Dropdown";
import { Card } from "@/components/ui/Card";
import { IdentifierCopy } from "@/components/ui/IdentifierCopy";
import { Tooltip } from "@/components/ui/Tooltip";

interface ObjectDetailViewProps {
  objectKey: string;
  parentObjectKey?: string;
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

  if (loading) {
    return (
      <div className="flex-1 flex flex-col min-h-screen bg-[#020408]">
        <PageHeader title="Loading..." />
        <div className="flex-1 flex items-center justify-center p-12">
          <div className="w-12 h-12 border-4 border-indigo-500/20 border-t-indigo-500 rounded-full animate-spin" />
        </div>
      </div>
    );
  }

  if (!object) {
    return (
      <div className="flex-1 flex flex-col min-h-screen bg-[#020408]">
        <PageHeader title="Object Not Found" />
        <div className="flex-1 flex flex-col items-center justify-center p-8 space-y-4 text-center">
          <div className="w-20 h-20 rounded-3xl bg-rose-500/10 flex items-center justify-center text-rose-500">
            <DocumentIcon className="w-10 h-10" />
          </div>
          <div>
            <h2 className="text-xl font-bold text-white uppercase tracking-wider">
              Missing Object
            </h2>
            <p className="text-slate-500 mt-2">
              The object you&apos;re looking for doesn&apos;t exist or you
              don&apos;t have access.
            </p>
          </div>
          <button
            onClick={() => router.push("/objects")}
            className="px-8 py-3 rounded-2xl bg-white/5 border border-white/10 text-white font-bold hover:bg-white/10 transition-all"
          >
            Return to Explorer
          </button>
        </div>
      </div>
    );
  }

  const fileName = object.key.split("/").pop() || object.key;
  const isImage = object.contentType?.startsWith("image/");

  return (
    <div className="flex-1 flex flex-col selection:bg-indigo-500/30">
      <div className="w-full mx-auto max-w-7xl px-6 lg:px-12">
        <PageHeader
          showDefaultActions={false}
          showBreadcrumbs={false}
          title={
            <div className="flex items-center gap-4 py-1">
              <button
                onClick={() => router.push("/objects")}
                className="p-2.5 rounded-2xl bg-white/5 text-slate-400 hover:text-white border border-white/5 hover:border-white/10 transition-all active:scale-95 group/back"
                title="Back to Objects"
              >
                <ArrowLeftIcon className="w-5 h-5 group-hover/back:-translate-x-0.5 transition-transform" />
              </button>
              <div className="space-y-0.5">
                <nav className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-slate-500">
                  <Link
                    href="/objects"
                    className="hover:text-indigo-400 transition-colors"
                  >
                    Objects
                  </Link>
                  <span className="text-white/10">/</span>
                  <span className="text-indigo-400/60 truncate max-w-[150px] font-mono">
                    {objectKey}
                  </span>
                </nav>
                <h1 className="text-2xl lg:text-3xl font-bold text-white tracking-tight uppercase">
                  {fileName}
                </h1>
              </div>
            </div>
          }
          actions={
            <div className="flex items-center gap-3">
              <button
                onClick={() => setIsEditing(true)}
                className="flex items-center gap-2 px-5 py-2.5 rounded-2xl bg-indigo-500/10 text-indigo-400 hover:bg-indigo-500/20 hover:text-indigo-300 text-xs font-bold uppercase tracking-wider transition-all border border-indigo-500/20 active:scale-[0.98]"
              >
                <PencilSquareIcon className="w-4 h-4" />
                <span className="hidden sm:inline">Edit Metadata</span>
              </button>

              <Tooltip content="Share object link" position="bottom">
                <button
                  onClick={handleShare}
                  className="group relative px-6 py-3 rounded-2xl bg-white/5 hover:bg-white/10 text-slate-400 hover:text-white text-xs font-bold uppercase tracking-wider transition-all border border-white/10 active:scale-95 flex items-center gap-2"
                >
                  <ShareIcon className="w-4 h-4 transition-transform group-hover:-translate-y-0.5" />
                  Share Link
                </button>
              </Tooltip>
              <Tooltip content="Synchronize to local storage" position="bottom">
                <button
                  onClick={handleDownload}
                  className="group relative px-6 py-3 rounded-2xl bg-emerald-500/10 hover:bg-emerald-600 text-emerald-500 hover:text-white text-xs font-bold uppercase tracking-wider transition-all border border-emerald-500/20 hover:border-emerald-600 shadow-lg active:scale-95 flex items-center gap-2"
                >
                  <ArrowDownTrayIcon className="w-4 h-4 transition-transform group-hover:translate-y-0.5" />
                  Download
                </button>
              </Tooltip>

              <Dropdown align="right" width="w-56">
                <Dropdown.Trigger
                  className="p-2.5 rounded-2xl transition-all border active:scale-95 bg-white/5 text-slate-400 border-white/5 hover:text-white hover:border-white/10"
                  activeClassName="bg-white/10 text-white border-white/20"
                >
                  <EllipsisHorizontalIcon className="w-5 h-5" />
                </Dropdown.Trigger>
                <Dropdown.Menu className="py-1">
                  <Dropdown.Item onClick={handleDownload}>
                    <div className="w-full text-left px-4 py-2.5 text-slate-300 hover:bg-white/5 hover:text-white flex items-center gap-3 transition-colors text-xs font-bold uppercase tracking-wider">
                      <ArrowDownTrayIcon className="w-4 h-4 text-indigo-400" />
                      Download
                    </div>
                  </Dropdown.Item>
                  <div className="my-1 h-px bg-white/10" />
                  {object.state === ObjectState.DELETED ? (
                    <Dropdown.Item onClick={() => setConfirmAction("restore")}>
                      <div className="w-full text-left px-4 py-2.5 text-emerald-400 hover:bg-emerald-500/10 flex items-center gap-3 transition-colors text-xs font-bold uppercase tracking-wider">
                        <ArrowPathIcon className="w-4 h-4" />
                        Restore Object
                      </div>
                    </Dropdown.Item>
                  ) : (
                    <Dropdown.Item onClick={() => setConfirmAction("trash")}>
                      <div className="w-full text-left px-4 py-2.5 text-amber-500 hover:bg-amber-500/10 flex items-center gap-3 transition-colors text-xs font-bold uppercase tracking-wider">
                        <TrashIcon className="w-4 h-4" />
                        Move to Trash
                      </div>
                    </Dropdown.Item>
                  )}
                  <Dropdown.Item onClick={() => setConfirmAction("purge")}>
                    <div className="w-full text-left px-4 py-2.5 text-rose-500 hover:bg-rose-500/10 flex items-center gap-3 transition-colors text-xs font-bold uppercase tracking-wider">
                      <XMarkIcon className="w-4 h-4" />
                      Permanently Delete
                    </div>
                  </Dropdown.Item>
                </Dropdown.Menu>
              </Dropdown>
            </div>
          }
        />
      </div>

      <main className="flex-1 w-full mx-auto p-6 lg:p-12 max-w-7xl">
        <div className="grid grid-cols-1 gap-8 lg:grid-cols-3">
          {/* Main Content Spine */}
          <div className="space-y-8 lg:col-span-2">
            {/* Object Preview Section */}
            <Card
              variant="glass"
              className="p-0 overflow-hidden group/preview border-indigo-500/10 shadow-2xl shadow-indigo-500/5"
            >
              <div className="relative bg-black/40 flex items-center justify-center p-8 min-h-[400px] lg:min-h-[500px]">
                <div className="absolute inset-0 bg-[radial-gradient(circle_at_50%_50%,rgba(99,102,241,0.08),transparent)] pointer-events-none" />
                <div className="absolute top-0 left-0 w-full h-px bg-gradient-to-r from-transparent via-indigo-500/20 to-transparent" />

                {isImage && downloadUrl ? (
                  <div className="relative w-full h-full flex items-center justify-center">
                    <div className="absolute inset-0 bg-indigo-500/5 blur-3xl rounded-full scale-75 opacity-50 group-hover/preview:scale-90 transition-transform duration-1000" />
                    <Image
                      src={downloadUrl.url}
                      alt={fileName}
                      width={800}
                      height={600}
                      className="relative z-10 max-h-[450px] w-auto object-contain rounded-2xl shadow-2xl border border-white/10 group-hover/preview:scale-[1.02] transition-transform duration-500"
                      loading="eager"
                      unoptimized
                    />
                  </div>
                ) : (
                  <div className="flex flex-col items-center gap-6">
                    <div className="w-24 h-24 rounded-3xl bg-white/5 flex items-center justify-center border border-white/10 shadow-inner">
                      <DocumentIcon className="w-12 h-12 text-slate-500 drop-shadow-lg" />
                    </div>
                    <div className="text-center space-y-2">
                      <p className="text-xs font-bold text-slate-400 uppercase tracking-wider">
                        No Preview Available
                      </p>
                      <p className="text-xs text-slate-600 font-bold uppercase tracking-wider">
                        Type: {object.contentType || "Binary Object"}
                      </p>
                    </div>
                  </div>
                )}
              </div>
            </Card>

            {/* Tags & Metadata Section */}
            <Card variant="glass" className="space-y-8">
              <div className="flex items-center justify-between px-2">
                <div className="flex items-center gap-4">
                  <div className="w-10 h-10 rounded-2xl bg-indigo-500/10 flex items-center justify-center text-indigo-400 border border-indigo-500/20">
                    <TagIcon className="w-5 h-5" />
                  </div>
                  <div>
                    <h3 className="text-xs font-bold text-white uppercase tracking-wider">
                      Object Tags
                    </h3>
                    <p className="text-xs text-slate-500 font-semibold uppercase tracking-wider mt-0.5">
                      Custom Metadata
                    </p>
                  </div>
                </div>
                <button
                  onClick={() => {
                    setIsEditing(!isEditing);
                    if (!isEditing) setEditingLabels({ ...object.tags });
                  }}
                  className={cn(
                    "px-4 py-2 rounded-xl text-xs font-bold uppercase tracking-wider transition-all border active:scale-95",
                    isEditing
                      ? "bg-rose-500/10 text-rose-500 border-rose-500/20 hover:bg-rose-500/20"
                      : "bg-white/5 text-slate-400 border-white/5 hover:text-white hover:border-white/10",
                  )}
                >
                  {isEditing ? "Cancel Edit" : "Edit Tags"}
                </button>
              </div>

              <div className="relative">
                {isEditing ? (
                  <div className="space-y-6 animate-fade-in px-2">
                    {/* New Label Form */}
                    <div className="flex flex-col gap-4 p-6 rounded-[28px] bg-indigo-500/5 border border-indigo-500/20 shadow-xl shadow-indigo-600/5">
                      <span className="text-xs font-semibold text-indigo-400 uppercase tracking-wider">
                        Add New Metadata Tag
                      </span>
                      <div className="grid grid-cols-2 gap-3">
                        <input
                          type="text"
                          placeholder="TAG_KEY"
                          value={newLabelKey}
                          onChange={(e) =>
                            setNewLabelKey(e.target.value.toUpperCase())
                          }
                          className="bg-black/40 border border-white/10 rounded-2xl px-4 py-3 text-xs font-mono text-white placeholder:text-slate-600 focus:border-indigo-500/50 outline-none transition-all"
                        />
                        <input
                          type="text"
                          placeholder="TAG_VALUE"
                          value={newLabelValue}
                          onChange={(e) => setNewLabelValue(e.target.value)}
                          className="bg-black/40 border border-white/10 rounded-2xl px-4 py-3 text-xs font-mono text-white placeholder:text-slate-600 focus:border-indigo-500/50 outline-none transition-all"
                        />
                      </div>
                      <button
                        onClick={addLabel}
                        disabled={!newLabelKey || !newLabelValue}
                        className="w-full py-3 rounded-2xl bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:hover:bg-indigo-600 text-white text-xs font-bold uppercase tracking-wider transition-all shadow-lg shadow-indigo-600/20 flex items-center justify-center gap-2 active:scale-[0.98]"
                      >
                        <PlusIcon className="w-4 h-4" />
                        Add Tag
                      </button>
                    </div>

                    {/* Edit List */}
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                      {Object.entries(editingLabels).map(([key, value]) => (
                        <div
                          key={key}
                          className="flex items-center gap-3 animate-slide-up"
                        >
                          <div className="flex-1 flex flex-col gap-1 p-4 rounded-2xl bg-black/40 border border-white/5">
                            <span className="text-xs font-semibold text-indigo-400/60 uppercase tracking-wider">
                              {key}
                            </span>
                            <span className="text-xs text-white font-mono break-all">
                              {value}
                            </span>
                          </div>
                          <button
                            onClick={() => removeLabel(key)}
                            className="p-4 rounded-2xl bg-rose-500/5 text-rose-500/40 hover:text-rose-500 hover:bg-rose-500/10 border border-rose-500/10 transition-all active:scale-90"
                          >
                            <TrashIcon className="w-5 h-5" />
                          </button>
                        </div>
                      ))}
                    </div>

                    <button
                      onClick={handleSaveLabels}
                      disabled={isSaving}
                      className="w-full py-4 rounded-3xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-bold uppercase tracking-wider transition-all shadow-xl shadow-emerald-600/20 flex items-center justify-center gap-3 active:scale-[0.98] mt-4"
                    >
                      {isSaving ? (
                        <div className="w-5 h-5 border-2 border-white/20 border-t-white rounded-full animate-spin" />
                      ) : (
                        <>
                          <CheckIcon className="w-5 h-5" />
                          Save Metadata
                        </>
                      )}
                    </button>
                  </div>
                ) : (
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-4 px-2">
                    {Object.entries(object.tags || {}).length > 0 ? (
                      Object.entries(object.tags).map(([key, value]) => (
                        <div
                          key={key}
                          className="group/label relative flex flex-col gap-2 p-5 rounded-[24px] bg-black/40 border border-white/5 hover:border-indigo-500/30 transition-all overflow-hidden"
                        >
                          <div className="absolute inset-0 bg-indigo-500/[0.04] opacity-0 group-hover/label:opacity-100 transition-opacity rounded-[32px]" />
                          <span className="relative text-xs font-semibold text-indigo-400/70 uppercase tracking-wider">
                            {key}
                          </span>
                          <span className="relative text-xs text-white font-mono font-bold leading-relaxed break-all">
                            {value}
                          </span>
                        </div>
                      ))
                    ) : (
                      <div className="col-span-2 py-12 text-center border-2 border-dashed border-white/5 rounded-[32px] bg-black/20">
                        <p className="text-xs text-slate-600 font-semibold uppercase tracking-wider">
                          No tags assigned
                        </p>
                      </div>
                    )}
                  </div>
                )}
              </div>
            </Card>

            {/* System Identifiers Section */}
            <Card variant="glass" className="space-y-6">
              <div className="flex items-center justify-between px-2">
                <div className="flex items-center gap-4">
                  <div className="w-10 h-10 rounded-2xl bg-indigo-500/10 flex items-center justify-center text-indigo-400 border border-indigo-500/20">
                    <ShieldCheckIcon className="w-5 h-5" />
                  </div>
                  <div>
                    <h3 className="text-xs font-bold text-white uppercase tracking-wider">
                      Unique Identifier
                    </h3>
                    <p className="text-xs text-slate-500 font-semibold uppercase tracking-wider mt-0.5">
                      MIME Type
                    </p>
                  </div>
                </div>
              </div>

              <div className="space-y-4 px-2">
                <IdentifierCopy
                  value={object?.objectId || objectKey}
                  label="Object UUID"
                />
                <div className="group/field relative items-center justify-between p-6 rounded-[24px] bg-black/40 border border-white/5 hover:border-white/10 transition-all">
                  <div className="space-y-2 min-w-0 mr-12">
                    <span className="flex items-center gap-2 text-xs font-semibold text-indigo-400/60 uppercase tracking-wider">
                      <GlobeAltIcon className="w-3.5 h-3.5" />
                      Storage Path
                    </span>
                    <p className="text-sm text-white font-mono break-all leading-relaxed select-all">
                      {object.key}
                    </p>
                  </div>
                  <button
                    onClick={() => handleCopy(object.key, "Object Key")}
                    className="absolute right-6 top-1/2 -translate-y-1/2 p-4 rounded-xl bg-white/5 text-slate-400 hover:text-white hover:bg-white/10 transition-all shrink-0 active:scale-95"
                  >
                    <ClipboardIcon className="w-5 h-5" />
                  </button>
                </div>
              </div>
            </Card>
          </div>

          {/* Sidebar: Technical Specs */}
          <aside className="space-y-8 lg:block">
            <Card variant="glass" className="space-y-6">
              <div className="flex items-center gap-4 px-2">
                <div className="w-10 h-10 rounded-2xl bg-emerald-500/10 flex items-center justify-center text-emerald-400 border border-emerald-500/20">
                  <ShieldCheckIcon className="w-5 h-5" />
                </div>
                <div>
                  <h3 className="text-xs font-bold text-white uppercase tracking-wider">
                    Technical Specs
                  </h3>
                  <p className="text-xs text-slate-500 font-semibold uppercase tracking-wider mt-0.5">
                    Persistence Details
                  </p>
                </div>
              </div>

              <div className="space-y-1 px-2">
                <div className="flex items-center justify-between py-4 border-b border-white/[0.03] group/item transition-colors hover:bg-white/[0.01] px-2 rounded-xl">
                  <span className="text-xs font-semibold text-slate-500 uppercase tracking-wider">
                    Storage ObjectKey
                  </span>
                  <Link
                    href={`/object-keys/${object.objectKey}`}
                    className="text-xs font-mono font-bold text-indigo-300 bg-black/40 px-2 py-1 rounded-lg border border-indigo-500/20 uppercase hover:bg-indigo-500/10 hover:border-indigo-500/30 transition-all"
                  >
                    {object.objectKey}
                  </Link>
                </div>
                <div className="flex items-center justify-between py-4 border-b border-white/[0.03] group/item transition-colors hover:bg-white/[0.01] px-2 rounded-xl">
                  <span className="text-xs font-semibold text-slate-500 uppercase tracking-wider">
                    Object Volume
                  </span>
                  <span className="text-sm font-bold text-indigo-400 tracking-tight">
                    {formatBytes(object.sizeBytes)}
                  </span>
                </div>
                <div className="flex items-center justify-between py-4 border-b border-white/[0.03] group/item transition-colors hover:bg-white/[0.01] px-2 rounded-xl">
                  <span className="text-xs font-semibold text-slate-500 uppercase tracking-wider">
                    Data Schema
                  </span>
                  <span className="text-xs font-mono font-bold text-slate-300 uppercase bg-white/5 px-2 py-1 rounded border border-white/10">
                    {object.contentType?.split("/")[1] || "BIN"}
                  </span>
                </div>
                <div className="flex flex-col py-4 border-b border-white/[0.03] group/item transition-colors hover:bg-white/[0.01] px-2 rounded-xl gap-2">
                  <span className="text-xs font-semibold text-slate-500 uppercase tracking-wider">
                    Global Status
                  </span>
                  <div className="flex items-center gap-2">
                    <ObjectTagBadge
                      objectTag={object.tags?.object_tag || "Untagged"}
                      linked={!!object.tags?.object_tag}
                    />
                    <span
                      className={cn(
                        "px-3 py-1 rounded-full text-xs font-bold uppercase tracking-wider border",
                        object.state === ObjectState.AVAILABLE
                          ? "bg-emerald-500/10 text-emerald-400 border-emerald-500/20"
                          : "bg-amber-500/10 text-amber-500 border-amber-500/20",
                      )}
                    >
                      {ObjectState[object.state]}
                    </span>
                  </div>
                </div>
                <div className="flex flex-col py-4 border-b border-white/[0.03] group/item transition-colors hover:bg-white/[0.01] px-2 rounded-xl gap-2">
                  <span className="text-xs font-semibold text-slate-500 uppercase tracking-wider">
                    Expiration Horizon
                  </span>
                  <div className="flex flex-col">
                    <span className="text-xs font-bold text-slate-200">
                      {object.presignExpiresAt?.seconds
                        ? new Date(
                            Number(object.presignExpiresAt.seconds) * 1000,
                          ).toLocaleDateString()
                        : "N/A"}
                    </span>
                    <span className="text-xs font-semibold text-slate-500 uppercase tracking-wider">
                      {object.presignExpiresAt?.seconds
                        ? new Date(
                            Number(object.presignExpiresAt.seconds) * 1000,
                          ).toLocaleTimeString()
                        : "N/A"}
                    </span>
                  </div>
                </div>
                <div className="flex items-center justify-between py-4 transition-colors hover:bg-white/[0.01] px-2 rounded-xl">
                  <span className="text-xs font-semibold text-slate-500 uppercase tracking-wider">
                    Reference Node
                  </span>
                  <span className="text-xs font-mono font-bold text-indigo-300 truncate max-w-[120px]">
                    {object.externalRef || "—"}
                  </span>
                </div>
              </div>
            </Card>
          </aside>
        </div>
      </main>

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
