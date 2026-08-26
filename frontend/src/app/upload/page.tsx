"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  ArrowUpTrayIcon,
  CheckCircleIcon,
  CloudArrowUpIcon,
  DocumentIcon,
  ExclamationCircleIcon,
  FolderIcon,
  PlusIcon,
  TagIcon,
  TrashIcon,
  XMarkIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useUpload, UploadTask } from "@/hooks/useUpload";
import { useCollections } from "@/hooks/useCollections";
import { formatBytes, cn } from "@/lib/utils";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import {
  SelectRoot,
  SelectContent,
  SelectItem,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";

// shadcn doesn't ship Progress by default in our install — fall back inline
// to a Tailwind div so we don't depend on an extra `add` step.
function ProgressBar({ value }: { value: number }) {
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-secondary">
      <div
        className="h-full bg-primary transition-[width] duration-500"
        style={{ width: `${Math.min(100, Math.max(0, value))}%` }}
      />
    </div>
  );
}

function QueueRow({ item }: { item: UploadTask }) {
  const meta = {
    pending: {
      icon: <DocumentIcon className="size-5" />,
      label: "Queued",
      color: "text-muted-foreground",
    },
    uploading: {
      icon: (
        <div className="relative size-5">
          <div className="absolute inset-0 rounded-full border-2 border-primary/20" />
          <div className="absolute inset-0 animate-spin rounded-full border-2 border-primary border-t-transparent" />
        </div>
      ),
      label: `${item.progress}%`,
      color: "text-primary",
    },
    completed: {
      icon: <CheckCircleIcon className="size-5" />,
      label: "Complete",
      color: "text-chart-2",
    },
    error: {
      icon: <ExclamationCircleIcon className="size-5" />,
      label: "Failed",
      color: "text-destructive",
    },
  } as const;
  const m = meta[item.status];

  return (
    <Card className="gap-2 py-3">
      <CardContent className="flex items-center gap-3 px-4">
        <div
          className={cn(
            "flex size-9 shrink-0 items-center justify-center rounded-md bg-secondary",
            m.color,
          )}
        >
          {m.icon}
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium">{item.file.name}</div>
          <div className="mt-0.5 flex items-center gap-3 text-xs">
            <span className="font-mono text-muted-foreground tabular-nums">
              {formatBytes(item.file.size)}
            </span>
            <span className={cn("font-medium", m.color)}>{m.label}</span>
            {item.error && (
              <span className="truncate text-destructive" title={item.error}>
                {item.error}
              </span>
            )}
          </div>
        </div>
      </CardContent>
      <div className="px-4">
        <ProgressBar value={item.progress} />
      </div>
    </Card>
  );
}

function TagPill({
  tagKey,
  tagValue,
  onRemove,
}: {
  tagKey: string;
  tagValue: string;
  onRemove: () => void;
}) {
  return (
    <Badge variant="secondary" className="gap-1.5 font-mono">
      <span className="text-chart-5">{tagKey}</span>
      <span className="text-muted-foreground">:</span>
      <span>{tagValue}</span>
      <button
        type="button"
        onClick={onRemove}
        className="ml-0.5 text-muted-foreground hover:text-destructive"
        aria-label={`Remove ${tagKey}`}
      >
        <XMarkIcon className="size-3" />
      </button>
    </Badge>
  );
}

export default function UploadPage() {
  const { queue, uploadFile, clearQueue } = useUpload();
  const router = useRouter();
  // The picker wants the whole set, not page one — see fetchAllCollections.
  const { collections, fetchAllCollections } = useCollections();
  const [selectedCollection, setSelectedCollection] = useState<string>("");
  // Sentinel routed through SelectRoot.onValueChange to mean "the user
  // clicked the footer affordance, not an actual Collection row". We
  // can't bind an onClick to a SelectItem because Radix swallows it
  // for the value selection — sentinel value is the documented seam.
  const NEW_OBJECT_KEY_SENTINEL = "__new__";
  const handleCollectionChange = (value: string) => {
    if (value === NEW_OBJECT_KEY_SENTINEL) {
      router.push("/collections");
      return;
    }
    setSelectedCollection(value);
  };
  const isLocked = !selectedCollection.trim();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [isDragging, setIsDragging] = useState(false);

  const [globalTags, setGlobalTags] = useState<Record<string, string>>({});
  const [tagInput, setTagInput] = useState("");

  useEffect(() => {
    void fetchAllCollections().then((res) => {
      if (
        res?.collections &&
        res.collections.length > 0 &&
        !selectedCollection
      ) {
        setSelectedCollection(res.collections[0].collection);
      }
    });
    // Intent: fetch once on mount, auto-select if nothing is chosen.
    // selectedCollection is read inside only as a guard — keeping it in
    // the deps re-fired this fetch on every dropdown selection.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fetchAllCollections]);

  const handleAddTag = useCallback(() => {
    if (!tagInput.includes(":")) return;
    const [k, v] = tagInput.split(":").map((s) => s.trim());
    if (k && v) {
      setGlobalTags((prev) => ({ ...prev, [k]: v }));
      setTagInput("");
    }
  }, [tagInput]);

  const removeTag = useCallback((k: string) => {
    setGlobalTags((prev) => {
      const next = { ...prev };
      delete next[k];
      return next;
    });
  }, []);

  const processFiles = useCallback(
    (files: FileList | null) => {
      if (isLocked || !files) return;
      Array.from(files).forEach((file) => {
        void uploadFile(file, selectedCollection.trim(), globalTags);
      });
    },
    [isLocked, selectedCollection, globalTags, uploadFile],
  );

  const onFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    processFiles(e.target.files);
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const onDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setIsDragging(false);
    processFiles(e.dataTransfer.files);
  };

  const completedCount = queue.filter((t) => t.status === "completed").length;
  const errorCount = queue.filter((t) => t.status === "error").length;
  const activeCount = queue.filter(
    (t) => t.status === "uploading" || t.status === "pending",
  ).length;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Upload"
        description="Stream files into a tenant Collection, optionally tagged."
        showDefaultActions={false}
      />

      <div className="mx-auto w-full max-w-5xl space-y-6">
        {/* Configuration */}
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          {/* Destination */}
          <Card>
            <CardHeader className="flex flex-row items-center gap-3 px-5">
              <div className="flex size-9 items-center justify-center rounded-md bg-primary/15 text-primary ring-1 ring-primary/30">
                <FolderIcon className="size-5" />
              </div>
              <div className="flex-1 space-y-0.5">
                <CardTitle className="text-sm">
                  Destination Collection
                </CardTitle>
                <CardDescription className="text-xs">
                  Required — picks the Collection the file will live under.
                </CardDescription>
              </div>
            </CardHeader>
            <CardContent className="px-5">
              <SelectRoot
                value={selectedCollection}
                onValueChange={handleCollectionChange}
              >
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="Select Collection…" />
                </SelectTrigger>
                <SelectContent>
                  {collections.map((ok) => (
                    <SelectItem key={ok.collection} value={ok.collection}>
                      {ok.displayName || ok.collection}
                    </SelectItem>
                  ))}
                  {/* Footer affordance — same pattern as the bucket
                      switcher's "Manage buckets…" entry. Picking this
                      row jumps to /collections (which owns the
                      provisioning dialog) instead of selecting a value;
                      see handleCollectionChange's sentinel. */}
                  <SelectSeparator />
                  <SelectItem
                    value={NEW_OBJECT_KEY_SENTINEL}
                    className="text-primary"
                  >
                    <PlusIcon className="size-4" />
                    New Collection
                  </SelectItem>
                </SelectContent>
              </SelectRoot>
            </CardContent>
          </Card>

          {/* Global tags */}
          <Card>
            <CardHeader className="flex flex-row items-center gap-3 px-5">
              <div className="flex size-9 items-center justify-center rounded-md bg-chart-5/15 text-chart-5 ring-1 ring-chart-5/30">
                <TagIcon className="size-5" />
              </div>
              <div className="flex-1 space-y-0.5">
                <CardTitle className="text-sm">
                  Global tags
                  {Object.keys(globalTags).length > 0 && (
                    <Badge variant="secondary" className="ml-2 font-normal">
                      {Object.keys(globalTags).length}
                    </Badge>
                  )}
                </CardTitle>
                <CardDescription className="text-xs">
                  Applied to every file uploaded in this session.
                </CardDescription>
              </div>
            </CardHeader>
            <CardContent className="space-y-3 px-5">
              <div className="flex items-center gap-2">
                <Label htmlFor="tag-input" className="sr-only">
                  Tag (key:value)
                </Label>
                <Input
                  id="tag-input"
                  type="text"
                  placeholder="key:value"
                  value={tagInput}
                  onChange={(e) => setTagInput(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && handleAddTag()}
                  className="flex-1 font-mono text-xs"
                />
                <Button
                  size="icon"
                  variant="outline"
                  onClick={handleAddTag}
                  disabled={!tagInput.includes(":")}
                  aria-label="Add tag"
                >
                  <PlusIcon className="size-4" />
                </Button>
              </div>
              {Object.keys(globalTags).length > 0 && (
                <div className="flex flex-wrap gap-2">
                  {Object.entries(globalTags).map(([k, v]) => (
                    <TagPill
                      key={k}
                      tagKey={k}
                      tagValue={v}
                      onRemove={() => removeTag(k)}
                    />
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </div>

        {/* Dropzone */}
        <div
          onDragOver={(e) => {
            e.preventDefault();
            if (!isLocked) setIsDragging(true);
          }}
          onDragLeave={() => setIsDragging(false)}
          onDrop={onDrop}
          onClick={() => !isLocked && fileInputRef.current?.click()}
          className={cn(
            "rounded-xl border-2 border-dashed p-12 text-center transition-colors md:p-16",
            isLocked
              ? "cursor-not-allowed border-border bg-card/40 opacity-60"
              : "cursor-pointer border-border bg-card/40 hover:border-primary/40",
            isDragging && "border-primary bg-primary/5",
          )}
        >
          <input
            type="file"
            multiple
            className="hidden"
            ref={fileInputRef}
            onChange={onFileChange}
            disabled={isLocked}
          />
          <div
            className={cn(
              "mx-auto flex size-14 items-center justify-center rounded-md ring-1 transition-colors",
              isLocked
                ? "bg-muted text-muted-foreground ring-border"
                : isDragging
                  ? "bg-primary text-primary-foreground ring-primary/40"
                  : "bg-primary/15 text-primary ring-primary/30",
            )}
          >
            {isLocked ? (
              <FolderIcon className="size-7" />
            ) : (
              <CloudArrowUpIcon className="size-7" />
            )}
          </div>
          {isLocked ? (
            <>
              <h2 className="mt-4 text-base font-semibold">
                Select a destination first
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                Choose a Collection above before uploading files.
              </p>
            </>
          ) : isDragging ? (
            <>
              <h2 className="mt-4 text-base font-semibold">
                Release to upload
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                Files will be uploaded to{" "}
                <span className="font-mono text-foreground">
                  {selectedCollection}
                </span>
              </p>
            </>
          ) : (
            <>
              <h2 className="mt-4 text-base font-semibold">
                Drop files or click to browse
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                Up to <span className="font-medium text-foreground">10 GB</span>{" "}
                per file.
              </p>
              <Button variant="outline" size="sm" className="mt-4">
                <ArrowUpTrayIcon className="size-4" />
                Browse files
              </Button>
            </>
          )}
        </div>

        {/* Queue */}
        {queue.length > 0 && (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex items-center gap-3 text-xs">
                <h3 className="text-sm font-medium text-muted-foreground">
                  Upload queue
                </h3>
                <Badge variant="secondary" className="font-mono">
                  {queue.length}
                </Badge>
                {activeCount > 0 && (
                  <span className="font-mono text-primary">
                    {activeCount} active
                  </span>
                )}
                {completedCount > 0 && (
                  <span className="font-mono text-chart-2">
                    {completedCount} done
                  </span>
                )}
                {errorCount > 0 && (
                  <span className="font-mono text-destructive">
                    {errorCount} failed
                  </span>
                )}
              </div>
              <Button size="sm" variant="ghost" onClick={clearQueue}>
                <TrashIcon className="size-3.5" />
                Clear
              </Button>
            </div>
            <Separator />
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              {queue.map((item) => (
                // Stable id, not name+index: new uploads are PREPENDED,
                // so an index-based key shifted every existing row and
                // remounted them (visible progress-bar flicker).
                <QueueRow key={item.id} item={item} />
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
