"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  ArrowUpTrayIcon,
  CloudArrowUpIcon,
  FolderIcon,
  PlusIcon,
  TagIcon,
  XMarkIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { UploadQueue } from "@/components/features/upload/UploadQueue";
import { useUpload } from "@/hooks/useUpload";
import { useFileDrop } from "@/hooks/useFileDrop";
import { useCollections } from "@/hooks/useCollections";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { cn } from "@/lib/utils";

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
import {
  SelectRoot,
  SelectContent,
  SelectItem,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";

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
  const {
    collections,
    error: collectionsError,
    fetchAllCollections,
  } = useCollections();
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
    (files: FileList | File[] | null) => {
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

  const { dragging: isDragging, dropProps } = useFileDrop({
    disabled: isLocked,
    onFiles: processFiles,
  });

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
            <CardContent className="space-y-2 px-5">
              {/* A failed read left only "New Collection" in the picker and the
                  dropzone locked — which nudged the operator to create a
                  Collection that may already exist. */}
              {collectionsError ? (
                <ListLoadError
                  variant="inline"
                  what="Collections"
                  reason={collectionsError}
                  onRetry={() => void fetchAllCollections()}
                />
              ) : null}
              <SelectRoot
                value={selectedCollection}
                onValueChange={handleCollectionChange}
              >
                <SelectTrigger
                  aria-label="Destination Collection"
                  className="w-full"
                >
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
          {...dropProps}
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

        <UploadQueue queue={queue} onClear={clearQueue} />
      </div>
    </div>
  );
}
