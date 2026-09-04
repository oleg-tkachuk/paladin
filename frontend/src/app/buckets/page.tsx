"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";

import { errorMessage } from "@/hooks/errorContract";
import {
  ArchiveBoxIcon,
  ArrowPathIcon,
  ArrowsUpDownIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  ClockIcon,
  EllipsisHorizontalIcon,
  MagnifyingGlassIcon,
  PlusIcon,
  ServerStackIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useBuckets } from "@/hooks/useBuckets";
import { useBackends } from "@/hooks/useBackends";
import { Bucket } from "@/gen/paladin/admin/v1/types_pb";
import { useNotification } from "@/components/ui/Notification";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/Card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  SelectRoot,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { searchFilter } from "@/lib/cel";
import { T } from "@/lib/ui/typography";
import { useTableSort, type SortState } from "@/hooks/useTableSort";

type SortColumn = "backend" | "name" | "region";

function SortHeader({
  label,
  column,
  current,
  onSort,
  className,
}: {
  label: string;
  column: SortColumn;
  current: SortState<SortColumn>;
  onSort: (c: SortColumn) => void;
  className?: string;
}) {
  const active = current.column === column && current.direction !== null;
  const Icon = !active
    ? ArrowsUpDownIcon
    : current.direction === "asc"
      ? ArrowUpIcon
      : ArrowDownIcon;
  return (
    <button
      type="button"
      onClick={() => onSort(column)}
      className={cn(
        "inline-flex items-center gap-1 text-xs font-medium uppercase tracking-wider transition-colors",
        active
          ? "text-foreground"
          : "text-muted-foreground hover:text-foreground",
        className,
      )}
    >
      {label}
      <Icon className="size-3.5 opacity-70" />
    </button>
  );
}

const ALL_BACKENDS = "__all__";

export default function BucketsPage() {
  const {
    buckets,
    loading,
    error: listError,
    fetchBuckets,
    createBucket,
    deleteBucket,
  } = useBuckets();
  const { backends: backendRows } = useBackends();
  const { showNotification } = useNotification();

  const backends = useMemo(
    () => backendRows.map((b) => b.backendId),
    [backendRows],
  );

  // ─── filters + sort ──────────────────────────────────────────────────────
  //
  // Search and the backend picker are SERVER-side now. This page used to fetch
  // every bucket — 859 of them, two requests of 500 — and filter the result in
  // the browser, so the whole table was pulled to answer a five-character
  // query. The filter goes to the API instead, as one conjunct over the derived
  // `search` field so it pushes down to SQL (see src/lib/cel.ts for why it must
  // not be a disjunction).
  const [search, setSearch] = useState("");
  // Trails `search` by 300ms so a refetch does not fire per keystroke — the
  // same shape the objects page uses.
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [filterBackend, setFilterBackend] = useState<string>(ALL_BACKENDS);
  const { sort, toggleSort: handleSort } = useTableSort<SortColumn>();

  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]);

  // ─── create ───────────────────────────────────────────────────────────────
  const [createOpen, setCreateOpen] = useState(false);
  const [newBackend, setNewBackend] = useState("");
  const [newName, setNewName] = useState("");
  const [newDisplayName, setNewDisplayName] = useState("");
  const [newRegion, setNewRegion] = useState("");
  const [submitting, setSubmitting] = useState(false);

  // ─── delete ───────────────────────────────────────────────────────────────
  const [deleteTarget, setDeleteTarget] = useState<Bucket | null>(null);
  const [deleteRemote, setDeleteRemote] = useState(false);

  // One place that knows how to ask, so the Refresh button and the error
  // retry cannot drift into fetching something other than what is on screen.
  const refetch = useCallback(
    () =>
      fetchBuckets(
        filterBackend === ALL_BACKENDS ? undefined : filterBackend,
        searchFilter(debouncedSearch),
      ),
    [fetchBuckets, filterBackend, debouncedSearch],
  );

  useEffect(() => {
    void refetch();
  }, [refetch]);

  // Default the create form to the first available backend once the dialog
  // opens and backends have loaded. Render-phase adjust-on-condition — the
  // !newBackend guard makes it converge in one extra render, so it's not a
  // set-state-in-effect.
  if (createOpen && !newBackend && backends.length > 0) {
    setNewBackend(backends[0]);
  }

  // Sort only. The two filters that used to live here are server-side now;
  // re-applying them in the browser would be dead code that quietly disagreed
  // with the API the day the two definitions of "matches" drifted apart.
  const filtered = useMemo(() => {
    let list = buckets;
    if (sort.column && sort.direction) {
      const dir = sort.direction === "asc" ? 1 : -1;
      list = [...list].sort((a, b) => {
        const va =
          sort.column === "backend"
            ? a.backendId
            : sort.column === "name"
              ? a.bucketId
              : a.region || "";
        const vb =
          sort.column === "backend"
            ? b.backendId
            : sort.column === "name"
              ? b.bucketId
              : b.region || "";
        return va < vb ? -dir : va > vb ? dir : 0;
      });
    }
    return list;
  }, [buckets, sort]);

  const hasFilters = search.trim() !== "" || filterBackend !== ALL_BACKENDS;
  // The debounce has not fired yet, or its refetch is still running.
  const searchPending = search.trim() !== debouncedSearch.trim() || loading;

  const handleCreate = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!newBackend || !newName) return;
    try {
      setSubmitting(true);
      await createBucket(newBackend, newName, newDisplayName, newRegion);
      showNotification({
        type: "success",
        title: "Bucket created",
        message: `${newName} (backend ${newBackend})`,
      });
      setNewName("");
      setNewDisplayName("");
      setNewRegion("");
      setCreateOpen(false);
    } catch (err) {
      showNotification({
        type: "error",
        title: "Creation failed",
        message: errorMessage(err, "Failed to create bucket."),
      });
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteBucket(
        deleteTarget.backendId,
        deleteTarget.bucketId,
        deleteTarget.resourceVersion,
        deleteRemote,
      );
      showNotification({
        type: "success",
        title: "Bucket deleted",
        message: `${deleteTarget.bucketId}${deleteRemote ? " (incl. S3)" : ""}`,
      });
      setDeleteTarget(null);
      setDeleteRemote(false);
    } catch (err) {
      showNotification({
        type: "error",
        title: "Deletion failed",
        message: errorMessage(err, "Failed to delete bucket."),
      });
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="S3 Buckets"
        description="Physical storage backends bound by Collections."
        showDefaultActions={false}
        actions={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <PlusIcon className="size-4" />
            New bucket
          </Button>
        }
      />

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[240px]">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Search by name or display label…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <SelectRoot value={filterBackend} onValueChange={setFilterBackend}>
          <SelectTrigger className="w-[200px]">
            <SelectValue placeholder="All backends" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_BACKENDS}>All backends</SelectItem>
            {backends.map((b) => (
              <SelectItem key={b} value={b}>
                {b}
              </SelectItem>
            ))}
          </SelectContent>
        </SelectRoot>
        <Button
          variant="outline"
          size="icon"
          onClick={() => void refetch()}
          aria-label="Refresh"
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>

      {/* Table */}
      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[180px]">
                <SortHeader
                  label="Backend"
                  column="backend"
                  current={sort}
                  onSort={handleSort}
                />
              </TableHead>
              <TableHead>
                <SortHeader
                  label="Bucket name"
                  column="name"
                  current={sort}
                  onSort={handleSort}
                />
              </TableHead>
              <TableHead className="hidden sm:table-cell">
                Display name
              </TableHead>
              <TableHead className="hidden md:table-cell">
                <SortHeader
                  label="Region"
                  column="region"
                  current={sort}
                  onSort={handleSort}
                />
              </TableHead>
              <TableHead className="w-[140px]">Status</TableHead>
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && buckets.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={6} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : listError ? (
              // "No buckets yet" for a failed list is a lie with consequences:
              // it invites the operator to create a bucket that already
              // exists.
              <TableRow>
                <TableCell colSpan={6} className="h-48 text-center">
                  <ListLoadError
                    what="Buckets"
                    reason={listError}
                    onRetry={() => void refetch()}
                  />
                </TableCell>
              </TableRow>
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="h-48 text-center">
                  <div className="flex flex-col items-center gap-3 text-muted-foreground">
                    <ArchiveBoxIcon className="size-10 opacity-40" />
                    <p className="text-sm">
                      {/*
                        Three states, not two. With the filter server-side an
                        empty table means one of: the debounce has not fired
                        yet, the request is in flight, or the server really
                        found nothing. Collapsing the first two into "No
                        buckets match" tells an operator their bucket is gone
                        while they are still typing its name.
                      */}
                      {searchPending
                        ? "Searching…"
                        : hasFilters
                          ? "No buckets match your filters."
                          : "No buckets yet."}
                    </p>
                    {!searchPending && !hasFilters && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => setCreateOpen(true)}
                      >
                        <PlusIcon className="size-4" />
                        Create the first bucket
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((b) => {
                // Drill into the storage-first browser: backend → bucket →
                // tenants → folders → files. The tenant-scoped path
                // (/tenants/.../buckets/.../collections) is for the
                // operator who already knows which tenant they care about;
                // /storage-backends/.../buckets/... is the "show me
                // what's physically here" view that scales naturally to
                // shared buckets with multiple tenant prefixes.
                const detailHref = `/storage-backends/${encodeURIComponent(b.backendId)}/buckets/${encodeURIComponent(b.bucketId)}`;
                return (
                  <TableRow
                    key={`${b.backendId}/${b.bucketId}`}
                    className="group"
                  >
                    <TableCell>
                      <Link
                        href={`/storage-backends/${encodeURIComponent(b.backendId)}`}
                        className="flex items-center gap-2 hover:text-primary"
                      >
                        <ServerStackIcon className="size-4 text-chart-5" />
                        <Badge variant="info" className={T.code}>
                          {b.backendId}
                        </Badge>
                      </Link>
                    </TableCell>
                    <TableCell>
                      <Link
                        href={detailHref}
                        className="flex items-center gap-3 hover:text-primary"
                      >
                        <div className="flex size-8 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
                          <ArchiveBoxIcon className="size-4" />
                        </div>
                        <span className="font-mono text-xs group-hover:underline">
                          {b.bucketId}
                        </span>
                      </Link>
                    </TableCell>
                    <TableCell className="hidden sm:table-cell font-medium">
                      <Link href={detailHref} className="hover:text-primary">
                        {b.displayName || (
                          <span className="text-muted-foreground italic">
                            —
                          </span>
                        )}
                      </Link>
                    </TableCell>
                    <TableCell className="hidden md:table-cell text-muted-foreground text-xs font-mono">
                      {b.region || "—"}
                    </TableCell>
                    <TableCell>
                      <ProvisionStateBadge state={b.provisionState} />
                    </TableCell>
                    <TableCell className="text-right">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-8 opacity-60 group-hover:opacity-100"
                            aria-label={`Actions for ${b.bucketId}`}
                          >
                            <EllipsisHorizontalIcon className="size-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          {/* Detail subtree lives under the owner tenant
                            (Phase 2 of the URL refactor). Shared
                            buckets — no owner_tenant_id — don't have
                            one, so we hide the link rather than
                            invent a placeholder route that 404s. */}
                          {b.ownerTenantId && (
                            <DropdownMenuItem asChild>
                              <Link
                                href={`/tenants/${encodeURIComponent(b.ownerTenantId)}/buckets/${encodeURIComponent(b.backendId)}/${encodeURIComponent(b.bucketId)}/lifecycle`}
                              >
                                <ClockIcon className="size-4" />
                                Lifecycle rules
                              </Link>
                            </DropdownMenuItem>
                          )}
                          <DropdownMenuItem
                            variant="destructive"
                            onSelect={() => setDeleteTarget(b)}
                          >
                            <TrashIcon className="size-4" />
                            Delete bucket
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>

      {/* ─── Create dialog ───────────────────────────────────────────────── */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <form onSubmit={handleCreate}>
            <DialogHeader>
              <DialogTitle>New S3 bucket</DialogTitle>
              <DialogDescription>
                Calls the underlying backend&apos;s CreateBucket API. The bucket
                becomes available as a target for new Collections — layout is{" "}
                <code className="font-mono text-foreground">
                  s3://&lt;bucket&gt;/&lt;tenant&gt;/&lt;object_key&gt;/&lt;key&gt;
                </code>
                .
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-4">
              <div className="space-y-1.5">
                <Label htmlFor="bucket-backend">Storage backend</Label>
                <SelectRoot
                  value={newBackend}
                  onValueChange={setNewBackend}
                  disabled={backends.length === 0}
                >
                  <SelectTrigger id="bucket-backend" className="w-full">
                    <SelectValue
                      placeholder={
                        backends.length === 0
                          ? "— no backends configured —"
                          : "Select a backend"
                      }
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {backends.map((b) => (
                      <SelectItem key={b} value={b}>
                        {b}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </SelectRoot>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="bucket-name">Bucket name</Label>
                <Input
                  id="bucket-name"
                  autoFocus
                  placeholder="paladin-primary"
                  className="font-mono text-xs"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value.toLowerCase())}
                />
                <p className={T.hint}>Lowercase, S3 naming rules apply.</p>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label htmlFor="bucket-display-name">Display name</Label>
                  <Input
                    id="bucket-display-name"
                    placeholder="Friendly label"
                    value={newDisplayName}
                    onChange={(e) => setNewDisplayName(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="bucket-region">Region</Label>
                  <Input
                    id="bucket-region"
                    placeholder="us-east-1"
                    className="font-mono text-xs"
                    value={newRegion}
                    onChange={(e) => setNewRegion(e.target.value)}
                  />
                </div>
              </div>
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="ghost"
                onClick={() => setCreateOpen(false)}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={submitting || !newBackend || !newName}
              >
                {submitting ? "Provisioning…" : "Create bucket"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* ─── Delete confirmation ─────────────────────────────────────────── */}
      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(o) => {
          if (!o) {
            setDeleteTarget(null);
            setDeleteRemote(false);
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this bucket?</AlertDialogTitle>
            <AlertDialogDescription>
              Removing{" "}
              <span className="font-mono text-foreground">
                {deleteTarget?.bucketId}
              </span>{" "}
              from backend{" "}
              <span className="font-mono text-foreground">
                {deleteTarget?.backendId}
              </span>
              . Any Collection still bound to it must be removed first.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <label className="flex cursor-pointer items-start gap-3 rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm">
            <Checkbox
              checked={deleteRemote}
              onCheckedChange={(v) => setDeleteRemote(v === true)}
              id="delete-remote"
              className="mt-0.5"
            />
            <div>
              <Label
                htmlFor="delete-remote"
                className="cursor-pointer text-destructive"
              >
                Also delete the physical S3 bucket
              </Label>
              <p className="mt-0.5 text-xs text-muted-foreground">
                The S3 bucket must already be empty for this to succeed.
              </p>
            </div>
          </label>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDelete}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Delete bucket
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

// ProvisionStateBadge mirrors the backend's outbox state machine. Empty
// or "ready" → muted "ready" pill; "pending" / "deleting" → animated
// info pill so the operator sees there's work in flight; "failed" /
// "deletion_failed" → destructive pill so a stuck row stands out at a
// glance. Old rows from before the migration default to "ready" — same
// affordance.
function ProvisionStateBadge({ state }: { state: string }) {
  const s = state || "ready";
  switch (s) {
    case "ready":
      return (
        <Badge
          variant="outline"
          className={cn(T.code, "text-muted-foreground")}
        >
          ready
        </Badge>
      );
    case "pending":
      return (
        <Badge variant="info" className={T.code}>
          provisioning…
        </Badge>
      );
    case "deleting":
      return (
        <Badge variant="warning" className={T.code}>
          deleting…
        </Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive" className={T.code}>
          failed
        </Badge>
      );
    case "deletion_failed":
      return (
        <Badge variant="destructive" className={T.code}>
          delete failed
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" className={T.code}>
          {s}
        </Badge>
      );
  }
}
