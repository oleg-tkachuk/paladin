"use client";

import { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  ArrowPathIcon,
  ArrowTopRightOnSquareIcon,
  MagnifyingGlassIcon,
  TagIcon,
  XMarkIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useObjectKeys } from "@/hooks/useObjectKeys";
import { useObjects } from "@/hooks/useObjects";
import { useScope } from "@/context/ScopeContext";
import { Select } from "@/components/ui/Select";
import { cn } from "@/lib/utils";

// /object-tags is a tenant-wide tag inventory. Editing happens on the
// per-object detail page (ObjectService.UpdateObject with the `tags`
// field mask) — this surface only shows what's already attached and
// jumps to the editor on click.
//
// We list objects in the active ObjectKey ("default" for now — the
// /object-keys switcher will scope this once it lands), aggregate the
// distinct (key, value) pairs across them, and render two views:
//
//   1. Tag inventory — chip cloud of every (key=value) seen, with a
//      per-pair count. Clicking a chip filters the object list below.
//   2. Object list — each object with its tag chips, a search box that
//      narrows by tag value or object key, and a row link that opens
//      the per-object editor.
//
// Read `?search=<chip>` from the URL so the ObjectTagBadge component's
// existing `linked` mode still works.

function objectIdFromName(name: string): string {
  // "tenants/{t}/objectKeys/{ok}/objects/{id}" → {id}
  const parts = name.split("/");
  return parts[parts.length - 1] ?? "";
}

function ObjectTagsPageInner({ initialSearch }: { initialSearch: string }) {
  const router = useRouter();
  // Filter resets when the parent passes a fresh `key={initialSearch}` —
  // see the export below — so chip-link navigations cleanly seed the
  // state without a setState-in-effect cascade.
  const [filter, setFilter] = useState(initialSearch);

  // Active scope — driven by ScopeContext.objectKey so the page tracks
  // whatever the user picked in /objects (or the dashboard's URL
  // links). Falls back to the first ObjectKey the tenant owns when
  // ScopeContext still points at the implicit "default" but no row
  // by that name exists — otherwise the page renders empty even when
  // objects exist under different keys, which is the most common
  // first-time-user confusion.
  const { objectKey: scopedObjectKey, setObjectKey } = useScope();
  const { objectKeys, fetchObjectKeys } = useObjectKeys();
  useEffect(() => {
    void fetchObjectKeys();
  }, [fetchObjectKeys]);
  const knownNames = useMemo(
    () => objectKeys.map((k) => k.objectKey),
    [objectKeys],
  );
  const objectKey = knownNames.includes(scopedObjectKey)
    ? scopedObjectKey
    : knownNames[0] || scopedObjectKey;

  const { objects, loading, refresh } = useObjects({
    objectKey,
    pageSize: 200,
  });

  // Aggregate the tag inventory. Map "key=value" → count so the chip
  // cloud is order-stable and clicking a chip can deterministically
  // filter the object list below.
  const inventory = useMemo(() => {
    const counts = new Map<string, number>();
    for (const obj of objects) {
      for (const [k, v] of Object.entries(obj.tags || {})) {
        const pair = `${k}=${v}`;
        counts.set(pair, (counts.get(pair) ?? 0) + 1);
      }
    }
    return Array.from(counts.entries()).sort((a, b) => {
      if (b[1] !== a[1]) return b[1] - a[1];
      return a[0].localeCompare(b[0]);
    });
  }, [objects]);

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return objects;
    return objects.filter((obj) => {
      if (obj.key.toLowerCase().includes(q)) return true;
      for (const [k, v] of Object.entries(obj.tags || {})) {
        if (`${k}=${v}`.toLowerCase().includes(q)) return true;
        if (k.toLowerCase().includes(q) || v.toLowerCase().includes(q))
          return true;
      }
      return false;
    });
  }, [objects, filter]);

  const handleChipClick = useCallback(
    (pair: string) => {
      // Toggle: clicking the active chip clears the filter.
      setFilter((prev) => (prev === pair ? "" : pair));
    },
    [setFilter],
  );

  const handleClear = useCallback(() => setFilter(""), [setFilter]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Object Tags"
        description="Tenant-wide inventory. Editing happens on the object detail page."
        showDefaultActions={false}
        actions={
          <div className="flex items-center gap-2">
            {knownNames.length > 0 && (
              <Select
                options={knownNames.map((n) => ({ value: n, label: n }))}
                value={objectKey}
                onChange={setObjectKey}
                className="min-w-[180px] font-mono text-xs"
              />
            )}
            <Button
              variant="outline"
              size="sm"
              onClick={() => void refresh()}
              disabled={loading}
            >
              <ArrowPathIcon
                className={cn("size-4", loading && "animate-spin")}
              />
              Refresh
            </Button>
          </div>
        }
      />

      {/* ─── Tag inventory ────────────────────────────────────────────── */}
      <Card className="space-y-3 p-4">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold">Tag inventory</h2>
            <p className="text-xs text-muted-foreground">
              {inventory.length === 0
                ? "No tags attached to any object yet."
                : `${inventory.length} distinct ${
                    inventory.length === 1 ? "pair" : "pairs"
                  } across ${objects.length} object${objects.length === 1 ? "" : "s"}.`}
            </p>
          </div>
        </div>
        {loading && objects.length === 0 ? (
          <Skeleton className="h-16 w-full" />
        ) : inventory.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            Open any object and add tags from{" "}
            <Link
              href="/objects"
              className="text-primary underline underline-offset-2"
            >
              the objects list
            </Link>{" "}
            — they&apos;ll show up here.
          </p>
        ) : (
          <div className="flex flex-wrap gap-2">
            {inventory.map(([pair, count]) => {
              const active = filter === pair;
              return (
                <button
                  key={pair}
                  type="button"
                  onClick={() => handleChipClick(pair)}
                  className={cn(
                    "inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs transition-colors",
                    active
                      ? "border-primary/50 bg-primary/15 text-primary"
                      : "border-border bg-muted/30 text-foreground hover:border-primary/30 hover:bg-primary/5",
                  )}
                >
                  <TagIcon className="size-3" />
                  <span className="font-mono">{pair}</span>
                  <span className="text-muted-foreground">·</span>
                  <span className="text-muted-foreground">{count}</span>
                </button>
              );
            })}
          </div>
        )}
      </Card>

      {/* ─── Object list with tag chips ──────────────────────────────── */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[240px]">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Filter by object key, tag key, or value (key=value)…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="pl-9"
          />
        </div>
        {filter && (
          <Button variant="outline" size="sm" onClick={handleClear}>
            <XMarkIcon className="size-4" />
            Clear
          </Button>
        )}
      </div>

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Object</TableHead>
              <TableHead>Tags</TableHead>
              <TableHead className="w-[120px] text-right">
                <span className="sr-only">Edit</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && objects.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={3} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={3} className="h-40 text-center">
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <TagIcon className="size-8 opacity-40" />
                    <p className="text-sm">
                      {filter
                        ? "No objects match this filter."
                        : "No objects yet."}
                    </p>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((obj) => {
                const id = objectIdFromName(obj.name);
                const detailHref = `/objects/${encodeURIComponent(
                  id,
                )}?objectKey=${encodeURIComponent(obj.objectKey)}`;
                const tagEntries = Object.entries(obj.tags || {});
                return (
                  <TableRow key={obj.name} className="group">
                    <TableCell>
                      <Link
                        href={detailHref}
                        className="font-mono text-xs hover:text-primary hover:underline"
                        title={obj.key}
                      >
                        {obj.key || (
                          <span className="italic text-muted-foreground">
                            (no key)
                          </span>
                        )}
                      </Link>
                    </TableCell>
                    <TableCell>
                      {tagEntries.length === 0 ? (
                        <span className="text-xs italic text-muted-foreground">
                          (untagged)
                        </span>
                      ) : (
                        <div className="flex flex-wrap gap-1">
                          {tagEntries.map(([k, v]) => (
                            <Badge
                              key={k}
                              variant="secondary"
                              className="font-mono text-[10px]"
                            >
                              {k}={v}
                            </Badge>
                          ))}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => router.push(detailHref)}
                        className="opacity-60 group-hover:opacity-100"
                      >
                        Edit
                        <ArrowTopRightOnSquareIcon className="size-3" />
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>
    </div>
  );
}

function ObjectTagsPageBoundary() {
  const searchParams = useSearchParams();
  const initial = searchParams.get("search") ?? "";
  // The `key` here forces the inner component to remount whenever the
  // URL ?search= changes — that resets `filter` without a
  // setState-in-effect cascade. Cheap because the children are small.
  return <ObjectTagsPageInner key={initial} initialSearch={initial} />;
}

export default function ObjectTagsPage() {
  // useSearchParams() requires a Suspense boundary in the App Router so
  // the page can be statically pre-rendered without burning the
  // search-param read at build time.
  return (
    <Suspense fallback={null}>
      <ObjectTagsPageBoundary />
    </Suspense>
  );
}
