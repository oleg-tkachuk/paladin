"use client";

import { useCallback, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ClipboardDocumentIcon,
  ExclamationTriangleIcon,
  KeyIcon,
  PlusIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { DurationSchema } from "@bufbuild/protobuf/wkt";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
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
import { Select } from "@/components/ui/Select";
import { useNotification } from "@/components/ui/Notification";
import { useScope } from "@/context/ScopeContext";
import { apiKeyClient } from "@/lib/connect/client";
import { copyToClipboard } from "@/lib/utils";
import type { ApiKey } from "@/gen/paladin/iam/v1/types_pb";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

// /api-tokens — non-interactive Personal Access Token surface backed
// by iam/v1.ApiKeyService.
//
//   • List   — ListApiKeys for the active tenant.
//   • Create — CreateApiKey returns the cleartext secret ONCE; the
//              dialog flips into a "save this now" reveal panel
//              after success and the user has to confirm before we
//              clear it from memory.
//   • Revoke — RevokeApiKey on a per-row Trash action.
//
// Tenant comes from the JWT, so the page never asks the operator to
// pick one. The role list is intentionally minimal (just
// platform.admin and tenant.admin) — finer granularity arrives
// alongside the scope picker (a follow-up).

const TTL_OPTIONS: { label: string; value: string; seconds: number | null }[] =
  [
    { label: "30 days", value: "30d", seconds: 30 * 24 * 3600 },
    { label: "90 days", value: "90d", seconds: 90 * 24 * 3600 },
    { label: "1 year", value: "365d", seconds: 365 * 24 * 3600 },
    { label: "Never expires", value: "never", seconds: null },
  ];

const ROLE_OPTIONS = [
  { value: "platform.admin", label: "platform.admin" },
  { value: "tenant.admin", label: "tenant.admin" },
  { value: "bucket.admin", label: "bucket.admin" },
];

function tenantParent(tenantId: string | null) {
  return tenantId ? `tenants/${tenantId}` : "";
}

function formatTimestamp(ts: { seconds: bigint } | undefined): string {
  if (!ts) return "—";
  const ms = Number(ts.seconds) * 1000;
  if (!ms) return "—";
  try {
    return new Date(ms).toISOString().replace("T", " ").replace(".000Z", "Z");
  } catch {
    return "—";
  }
}

export default function ApiTokensPage() {
  const { tenantId } = useScope();
  const { showNotification } = useNotification();
  const parent = tenantParent(tenantId);

  // ── list state ──────────────────────────────────────────────────────
  const [includeRevoked, setIncludeRevoked] = useState(false);

  const keysQuery = useQuery({
    queryKey: ["apiTokens", parent, includeRevoked],
    enabled: !!parent,
    // retry off: the queryFn toasts on failure, a retry would double-toast.
    retry: false,
    queryFn: async ({ signal }) => {
      try {
        const res = await apiKeyClient.listApiKeys(
          { parent, includeRevoked },
          { signal },
        );
        return res.apiKeys;
      } catch (err) {
        showNotification({
          type: "error",
          title: "Load failed",
          message:
            err instanceof ConnectError
              ? err.rawMessage
              : "Failed to list API tokens",
        });
        throw err;
      }
    },
  });
  const keys = useMemo(() => keysQuery.data ?? [], [keysQuery.data]);
  const loading = keysQuery.isFetching;
  const refreshKeys = () => void keysQuery.refetch();

  // ── create dialog ───────────────────────────────────────────────────
  const [createOpen, setCreateOpen] = useState(false);
  const [description, setDescription] = useState("");
  const [role, setRole] = useState("tenant.admin");
  const [ttlValue, setTtlValue] = useState("90d");
  const [creating, setCreating] = useState(false);

  // After success: { secret, displayPrefix } until the user dismisses.
  const [reveal, setReveal] = useState<{
    secret: string;
    displayPrefix: string;
  } | null>(null);
  const [revealAcknowledged, setRevealAcknowledged] = useState(false);

  const resetCreateForm = () => {
    setDescription("");
    setRole("tenant.admin");
    setTtlValue("90d");
    setReveal(null);
    setRevealAcknowledged(false);
  };

  const handleCreate = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!parent || !description.trim()) return;
    const ttlSeconds = TTL_OPTIONS.find((o) => o.value === ttlValue)?.seconds;
    setCreating(true);
    try {
      const res = await apiKeyClient.createApiKey({
        parent,
        description: description.trim(),
        roles: [role],
        scopes: [],
        ttl:
          ttlSeconds === null || ttlSeconds === undefined
            ? undefined
            : create(DurationSchema, {
                seconds: BigInt(ttlSeconds),
                nanos: 0,
              }),
      });
      setReveal({
        secret: res.secret,
        displayPrefix: res.apiKey?.displayPrefix ?? "",
      });
      // The reveal dialog stays open showing the secret; closing it calls
      // refreshKeys(), which picks up the new key with server timestamps.
      showNotification({
        type: "success",
        title: "Token created",
        message: "Copy the secret now — it can't be shown again.",
      });
    } catch (err) {
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Create failed";
      showNotification({
        type: "error",
        title: "Create failed",
        message: msg,
      });
    } finally {
      setCreating(false);
    }
  };

  const handleCloseCreateDialog = (open: boolean) => {
    if (open) {
      setCreateOpen(true);
      return;
    }
    if (reveal && !revealAcknowledged) return; // block close until acked
    setCreateOpen(false);
    resetCreateForm();
    refreshKeys();
  };

  const copySecret = useCallback(async () => {
    if (!reveal) return;
    const ok = await copyToClipboard(reveal.secret);
    showNotification({
      type: ok ? "success" : "error",
      title: ok ? "Copied" : "Copy failed",
      message: ok
        ? "Token secret copied to clipboard."
        : "Clipboard unavailable.",
    });
  }, [reveal, showNotification]);

  // ── revoke ──────────────────────────────────────────────────────────
  const [revokeTarget, setRevokeTarget] = useState<ApiKey | null>(null);
  const handleRevoke = async () => {
    if (!revokeTarget) return;
    try {
      await apiKeyClient.revokeApiKey({ name: revokeTarget.name });
      showNotification({
        type: "success",
        title: "Token revoked",
        message: revokeTarget.displayPrefix || revokeTarget.apiKeyId,
      });
      setRevokeTarget(null);
      refreshKeys();
    } catch (err) {
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Revoke failed";
      showNotification({
        type: "error",
        title: "Revoke failed",
        message: msg,
      });
    }
  };

  const visibleKeys = useMemo(
    () => (includeRevoked ? keys : keys.filter((k) => !k.revoked)),
    [keys, includeRevoked],
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title="API Tokens"
        description="Personal access tokens for programmatic access to the control plane."
        showDefaultActions={false}
        actions={
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={refreshKeys}
              disabled={loading}
            >
              <ArrowPathIcon
                className={cn("size-4", loading && "animate-spin")}
              />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <PlusIcon className="size-4" />
              New token
            </Button>
          </div>
        }
      />

      <div className="flex items-center gap-3 text-sm">
        <label className="flex cursor-pointer items-center gap-2">
          <input
            type="checkbox"
            checked={includeRevoked}
            onChange={(e) => setIncludeRevoked(e.target.checked)}
            className="size-4 accent-primary"
          />
          <span className="text-muted-foreground">Show revoked</span>
        </label>
      </div>

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[180px]">Prefix</TableHead>
              <TableHead>Description</TableHead>
              <TableHead>Roles</TableHead>
              <TableHead className="hidden md:table-cell">Created</TableHead>
              <TableHead className="hidden lg:table-cell">Expires</TableHead>
              <TableHead className="w-[100px]">Status</TableHead>
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && visibleKeys.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={7} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : visibleKeys.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="h-40 text-center">
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <KeyIcon className="size-8 opacity-40" />
                    <p className="text-sm">No API tokens yet.</p>
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => setCreateOpen(true)}
                    >
                      <PlusIcon className="size-4" />
                      Create the first one
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              visibleKeys.map((k) => (
                <TableRow key={k.name} className="group">
                  <TableCell>
                    <span className="font-mono text-xs">
                      {k.displayPrefix || "—"}
                    </span>
                  </TableCell>
                  <TableCell className="font-medium">
                    {k.description || (
                      <span className="italic text-muted-foreground">
                        (no description)
                      </span>
                    )}
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {k.roles.map((r) => (
                        <Badge
                          key={r}
                          variant="outline"
                          className={cn(T.labelTight, "font-mono")}
                        >
                          {r}
                        </Badge>
                      ))}
                    </div>
                  </TableCell>
                  <TableCell
                    className={cn(
                      "hidden md:table-cell",
                      T.code,
                      "text-muted-foreground",
                    )}
                  >
                    {formatTimestamp(k.createdAt)}
                  </TableCell>
                  <TableCell
                    className={cn(
                      "hidden lg:table-cell",
                      T.code,
                      "text-muted-foreground",
                    )}
                  >
                    {k.expiresAt ? formatTimestamp(k.expiresAt) : "never"}
                  </TableCell>
                  <TableCell>
                    {k.revoked ? (
                      <Badge variant="destructive" className={T.code}>
                        revoked
                      </Badge>
                    ) : (
                      <Badge variant="success" className={T.code}>
                        active
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-right">
                    {!k.revoked && (
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-8 opacity-60 group-hover:opacity-100"
                        aria-label={`Revoke ${k.displayPrefix || k.apiKeyId}`}
                        onClick={() => setRevokeTarget(k)}
                      >
                        <TrashIcon className="size-4" />
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>

      {/* ─── Create dialog ───────────────────────────────────────────── */}
      <Dialog open={createOpen} onOpenChange={handleCloseCreateDialog}>
        <DialogContent>
          {reveal ? (
            <div>
              <DialogHeader>
                <DialogTitle className="flex items-center gap-2">
                  <CheckCircleIcon className="size-5 text-emerald-500" />
                  Token created
                </DialogTitle>
                <DialogDescription>
                  Copy the secret now — the control plane only stores its hash,
                  so this is the last time it will be visible.
                </DialogDescription>
              </DialogHeader>
              <div className="space-y-3 py-4">
                <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">
                  <ExclamationTriangleIcon className="mr-1 inline-block size-4 align-text-bottom" />
                  Save this in a password manager or secret store. We can&apos;t
                  show it again.
                </div>
                <div className="space-y-1.5">
                  <Label className="text-xs">Token secret</Label>
                  <div className="flex gap-2">
                    <Input
                      readOnly
                      value={reveal.secret}
                      className="font-mono text-xs"
                      onFocus={(e) => e.currentTarget.select()}
                    />
                    <Button
                      variant="outline"
                      onClick={copySecret}
                      type="button"
                    >
                      <ClipboardDocumentIcon className="size-4" />
                      Copy
                    </Button>
                  </div>
                  <p className={cn(T.code, "text-muted-foreground")}>
                    prefix: {reveal.displayPrefix}
                  </p>
                </div>
                <label className="flex cursor-pointer items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={revealAcknowledged}
                    onChange={(e) => setRevealAcknowledged(e.target.checked)}
                    className="size-4 accent-primary"
                  />
                  <span>I&apos;ve copied this token somewhere safe.</span>
                </label>
              </div>
              <DialogFooter>
                <Button
                  type="button"
                  onClick={() => handleCloseCreateDialog(false)}
                  disabled={!revealAcknowledged}
                >
                  Done
                </Button>
              </DialogFooter>
            </div>
          ) : (
            <form onSubmit={handleCreate}>
              <DialogHeader>
                <DialogTitle>New API token</DialogTitle>
                <DialogDescription>
                  Grants programmatic access to the active tenant. Token is
                  signed at issue time; revoke at any moment from this page.
                </DialogDescription>
              </DialogHeader>
              <div className="space-y-4 py-4">
                <div className="space-y-1.5">
                  <Label htmlFor="tok-desc">Description</Label>
                  <Input
                    id="tok-desc"
                    autoFocus
                    placeholder="ci-deploy, terraform-bot, …"
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                  />
                  <p className={T.hint}>
                    Free-form label so you can find the token later in audit
                    logs.
                  </p>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="tok-role">Role</Label>
                  <Select
                    options={ROLE_OPTIONS}
                    value={role}
                    onChange={setRole}
                    className="w-full"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="tok-ttl">Expiration</Label>
                  <Select
                    options={TTL_OPTIONS.map((o) => ({
                      value: o.value,
                      label: o.label,
                    }))}
                    value={ttlValue}
                    onChange={setTtlValue}
                    className="w-full"
                  />
                </div>
              </div>
              <DialogFooter>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => handleCloseCreateDialog(false)}
                >
                  Cancel
                </Button>
                <Button
                  type="submit"
                  disabled={creating || !description.trim() || !parent}
                >
                  {creating ? "Creating…" : "Create token"}
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>

      {/* ─── Revoke confirm ────────────────────────────────────────── */}
      <AlertDialog
        open={!!revokeTarget}
        onOpenChange={(o) => !o && setRevokeTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke this token?</AlertDialogTitle>
            <AlertDialogDescription>
              The token{" "}
              <span className="font-mono text-foreground">
                {revokeTarget?.displayPrefix}
              </span>{" "}
              will stop accepting traffic immediately. This cannot be undone —
              create a new token if access is needed again.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleRevoke}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Revoke
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
