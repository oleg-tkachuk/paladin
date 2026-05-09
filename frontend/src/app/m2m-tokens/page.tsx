"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ClipboardDocumentIcon,
  ExclamationTriangleIcon,
  KeyIcon,
  PlusIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { Code, ConnectError } from "@connectrpc/connect";

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
import { apiTokenClient } from "@/lib/connect/client";
import { copyToClipboard, cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { APIToken } from "@/gen/paladin/admin/v1/api_token_service_pb";

// /m2m-tokens — service-to-service hashed-bearer tokens.
//
// Distinct from /api-tokens (legacy iam.ApiKey, user-scoped PATs).
// These are admin/v1.APITokenService tokens following the GitHub PAT /
// Stripe / Hatchet pattern: argon2id PHC hash on the server, plaintext
// returned exactly once. Scoped per-tenant + per-audience + per-role,
// optionally rate-limited via a sliding-window counter.
//
//   • List   — APITokenService.List for the active tenant.
//   • Create — APITokenService.Create returns the cleartext token ONCE.
//              Dialog flips into a reveal panel; user must ack before
//              we drop it from memory.
//   • Revoke — APITokenService.Revoke; idempotent on the server.
//
//   • Usage  — APITokenService.GetUsage; sliding-window snapshot
//              fetched per-token after List. NOT_FOUND ⇒ token has
//              never been verified ("never"). Rendered as
//              weighted / limit with "resets in Xs" subtitle.

const TTL_OPTIONS: { label: string; value: string; seconds: number | null }[] =
  [
    { label: "30 days", value: "30d", seconds: 30 * 24 * 3600 },
    { label: "90 days", value: "90d", seconds: 90 * 24 * 3600 },
    { label: "1 year", value: "365d", seconds: 365 * 24 * 3600 },
    { label: "Server default", value: "default", seconds: null },
  ];

const AUDIENCE_CHOICES = ["data", "admin", "iam", "mcp"];

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

function isRevoked(t: APIToken): boolean {
  if (!t.revokedAt) return false;
  return Number(t.revokedAt.seconds) > 0;
}

function isExpired(t: APIToken): boolean {
  if (!t.expiresAt) return false;
  const ms = Number(t.expiresAt.seconds) * 1000;
  return ms > 0 && ms < Date.now();
}

export default function M2MTokensPage() {
  const { tenantId } = useScope();
  const { showNotification } = useNotification();

  // ── list state ──────────────────────────────────────────────────────
  const [tokens, setTokens] = useState<APIToken[]>([]);
  const [loading, setLoading] = useState(false);
  const [includeRevoked, setIncludeRevoked] = useState(false);
  const [includeExpired, setIncludeExpired] = useState(false);

  // Per-token sliding-window snapshot fetched after list. Map keyed
  // by token.id; "never" sentinel for NOT_FOUND (token never used).
  // Re-fetched whenever the list refreshes; UI renders weighted /
  // limit with a "resets in Ns" subtitle.
  type UsageSnap = {
    limitRpm: number;
    weighted: number;
    currentBucket: bigint;
    resetsAtMs: number;
  };
  const [usage, setUsage] = useState<Map<string, UsageSnap | "never">>(
    new Map(),
  );

  const fetchTokens = useCallback(async () => {
    if (!tenantId) return;
    setLoading(true);
    try {
      const res = await apiTokenClient.list({
        tenantId,
        includeRevoked,
        includeExpired,
        pageSize: 100,
      });
      setTokens(res.apiTokens);

      // Fan-out usage fetches for non-revoked tokens. Revoked
      // tokens have no live counters worth showing.
      void Promise.all(
        res.apiTokens.map(async (t) => {
          if (isRevoked(t)) return null;
          try {
            const u = await apiTokenClient.getUsage({ id: t.id });
            const resetsAtMs = u.windowResetsAt
              ? Number(u.windowResetsAt.seconds) * 1000
              : 0;
            return [
              t.id,
              {
                limitRpm: u.limitRpm,
                weighted: u.weightedCount,
                currentBucket: u.currentBucketCount,
                resetsAtMs,
              },
            ] as const;
          } catch (err) {
            if (err instanceof ConnectError && err.code === Code.NotFound) {
              return [t.id, "never" as const] as const;
            }
            return null;
          }
        }),
      ).then((entries) => {
        setUsage(
          new Map(
            entries.filter(
              (e): e is readonly [string, UsageSnap | "never"] => e !== null,
            ),
          ),
        );
      });
    } catch (err) {
      const msg =
        err instanceof ConnectError
          ? err.rawMessage
          : "Failed to list M2M tokens";
      showNotification({
        type: "error",
        title: "Load failed",
        message: msg,
      });
    } finally {
      setLoading(false);
    }
  }, [tenantId, includeRevoked, includeExpired, showNotification]);

  useEffect(() => {
    void fetchTokens();
  }, [fetchTokens]);

  // ── create dialog ───────────────────────────────────────────────────
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState("");
  const [audience, setAudience] = useState<Set<string>>(new Set(["data"]));
  const [ttl, setTtl] = useState("90d");
  const [rateLimit, setRateLimit] = useState("0");
  const [creating, setCreating] = useState(false);

  // After success: { token, prefix } until the user dismisses.
  const [reveal, setReveal] = useState<{
    token: string;
    prefix: string;
  } | null>(null);
  const [revealAcknowledged, setRevealAcknowledged] = useState(false);

  const resetCreateForm = () => {
    setName("");
    setScopes("");
    setAudience(new Set(["data"]));
    setTtl("90d");
    setRateLimit("0");
    setReveal(null);
    setRevealAcknowledged(false);
  };

  const toggleAudience = (aud: string) => {
    const next = new Set(audience);
    if (next.has(aud)) next.delete(aud);
    else next.add(aud);
    setAudience(next);
  };

  const handleCreate = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!tenantId || !name.trim()) return;
    if (audience.size === 0) {
      showNotification({
        type: "error",
        title: "Validation",
        message: "At least one audience plane must be selected.",
      });
      return;
    }
    const ttlSeconds = TTL_OPTIONS.find((o) => o.value === ttl)?.seconds;
    const scopeList = scopes
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    const rpm = Number.parseInt(rateLimit || "0", 10);

    setCreating(true);
    try {
      const res = await apiTokenClient.create({
        tenantId,
        name: name.trim(),
        ttlSeconds: BigInt(ttlSeconds ?? 0),
        scopes: scopeList,
        audience: Array.from(audience),
        rateLimitRpm: Number.isFinite(rpm) && rpm > 0 ? rpm : 0,
      });
      setReveal({
        token: res.token,
        prefix: res.apiToken?.prefix ?? "",
      });
      if (res.apiToken) {
        setTokens((prev) => [res.apiToken!, ...prev]);
      }
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
    if (reveal && !revealAcknowledged) return;
    setCreateOpen(false);
    resetCreateForm();
    void fetchTokens();
  };

  const copyToken = useCallback(async () => {
    if (!reveal) return;
    const ok = await copyToClipboard(reveal.token);
    showNotification({
      type: ok ? "success" : "error",
      title: ok ? "Copied" : "Copy failed",
      message: ok
        ? "Token secret copied to clipboard."
        : "Clipboard unavailable.",
    });
  }, [reveal, showNotification]);

  // ── revoke ──────────────────────────────────────────────────────────
  const [revokeTarget, setRevokeTarget] = useState<APIToken | null>(null);
  const handleRevoke = async () => {
    if (!revokeTarget) return;
    try {
      await apiTokenClient.revoke({ id: revokeTarget.id });
      showNotification({
        type: "success",
        title: "Token revoked",
        message: revokeTarget.name || revokeTarget.prefix,
      });
      setRevokeTarget(null);
      void fetchTokens();
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

  const visibleTokens = useMemo(() => tokens, [tokens]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="M2M Tokens"
        description="Service-to-service hashed-bearer tokens (admin/v1.APITokenService). Tenant-scoped, audience-pinned, optionally rate-limited."
        showDefaultActions={false}
        actions={
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => void fetchTokens()}
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

      <div className="flex items-center gap-4 text-sm">
        <label className="flex cursor-pointer items-center gap-2">
          <input
            type="checkbox"
            checked={includeRevoked}
            onChange={(e) => setIncludeRevoked(e.target.checked)}
            className="size-4 accent-primary"
          />
          <span className="text-muted-foreground">Show revoked</span>
        </label>
        <label className="flex cursor-pointer items-center gap-2">
          <input
            type="checkbox"
            checked={includeExpired}
            onChange={(e) => setIncludeExpired(e.target.checked)}
            className="size-4 accent-primary"
          />
          <span className="text-muted-foreground">Show expired</span>
        </label>
      </div>

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[140px]">Prefix</TableHead>
              <TableHead>Name</TableHead>
              <TableHead className="hidden md:table-cell">Audience</TableHead>
              <TableHead className="hidden lg:table-cell">Rate (rpm)</TableHead>
              <TableHead className="hidden xl:table-cell w-[140px]">
                Usage (1m)
              </TableHead>
              <TableHead className="hidden lg:table-cell">Last used</TableHead>
              <TableHead className="hidden lg:table-cell">Expires</TableHead>
              <TableHead className="w-[100px]">Status</TableHead>
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && visibleTokens.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={9} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : visibleTokens.length === 0 ? (
              <TableRow>
                <TableCell colSpan={9} className="h-40 text-center">
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <KeyIcon className="size-8 opacity-40" />
                    <p className="text-sm">No M2M tokens yet.</p>
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
              visibleTokens.map((t) => {
                const revoked = isRevoked(t);
                const expired = isExpired(t);
                return (
                  <TableRow key={t.id} className="group">
                    <TableCell>
                      <span className="font-mono text-xs">
                        paladin_pat_{t.prefix}…
                      </span>
                    </TableCell>
                    <TableCell className="font-medium">
                      {t.name || (
                        <span className="italic text-muted-foreground">
                          (unnamed)
                        </span>
                      )}
                      {t.scopes.length > 0 && (
                        <div className="mt-1 flex flex-wrap gap-1">
                          {t.scopes.map((s) => (
                            <Badge
                              key={s}
                              variant="outline"
                              className={cn(T.labelTight, "font-mono")}
                            >
                              {s}
                            </Badge>
                          ))}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <div className="flex flex-wrap gap-1">
                        {t.audience.map((a) => (
                          <Badge
                            key={a}
                            variant="secondary"
                            className={T.labelTight}
                          >
                            {a}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell
                      className={cn(
                        "hidden lg:table-cell",
                        T.code,
                        "text-muted-foreground",
                      )}
                    >
                      {t.rateLimitRpm > 0 ? t.rateLimitRpm : "∞"}
                    </TableCell>
                    <TableCell className={cn("hidden xl:table-cell", T.code)}>
                      {(() => {
                        if (revoked) {
                          return (
                            <span className="text-muted-foreground">—</span>
                          );
                        }
                        const u = usage.get(t.id);
                        if (u === undefined) {
                          return (
                            <span className="text-muted-foreground">…</span>
                          );
                        }
                        if (u === "never") {
                          return (
                            <span className="text-muted-foreground">
                              never used
                            </span>
                          );
                        }
                        const resetsInS = Math.max(
                          0,
                          Math.round((u.resetsAtMs - Date.now()) / 1000),
                        );
                        const limit = u.limitRpm > 0 ? u.limitRpm : 0;
                        const weighted = u.weighted.toFixed(1);
                        const overCap = limit > 0 && u.weighted >= limit * 0.9;
                        return (
                          <div className="space-y-0.5">
                            <div className={overCap ? "text-warning" : ""}>
                              {weighted}
                              {limit > 0 ? (
                                <span className="text-muted-foreground">
                                  {" / "}
                                  {limit}
                                </span>
                              ) : (
                                <span className="text-muted-foreground">
                                  {" / ∞"}
                                </span>
                              )}
                            </div>
                            <div className={T.hint}>resets in {resetsInS}s</div>
                          </div>
                        );
                      })()}
                    </TableCell>
                    <TableCell
                      className={cn(
                        "hidden lg:table-cell",
                        T.code,
                        "text-muted-foreground",
                      )}
                    >
                      {t.lastUsedAt ? formatTimestamp(t.lastUsedAt) : "never"}
                    </TableCell>
                    <TableCell
                      className={cn(
                        "hidden lg:table-cell",
                        T.code,
                        "text-muted-foreground",
                      )}
                    >
                      {t.expiresAt ? formatTimestamp(t.expiresAt) : "never"}
                    </TableCell>
                    <TableCell>
                      {revoked ? (
                        <Badge variant="destructive" className={T.code}>
                          revoked
                        </Badge>
                      ) : expired ? (
                        <Badge variant="outline" className={T.code}>
                          expired
                        </Badge>
                      ) : (
                        <Badge variant="success" className={T.code}>
                          active
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      {!revoked && (
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-8 opacity-60 group-hover:opacity-100"
                          aria-label={`Revoke ${t.name || t.prefix}`}
                          onClick={() => setRevokeTarget(t)}
                        >
                          <TrashIcon className="size-4" />
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>

      {/* ─── Create dialog ───────────────────────────────────────────── */}
      <Dialog open={createOpen} onOpenChange={handleCloseCreateDialog}>
        <DialogContent className="max-w-xl">
          {reveal ? (
            <div>
              <DialogHeader>
                <DialogTitle className="flex items-center gap-2">
                  <CheckCircleIcon className="size-5 text-emerald-500" />
                  Token created
                </DialogTitle>
                <DialogDescription>
                  Copy the secret now — the control plane only stores its
                  argon2id hash, so this is the last time it will be visible.
                </DialogDescription>
              </DialogHeader>
              <div className="space-y-3 py-4">
                <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">
                  <ExclamationTriangleIcon className="mr-1 inline-block size-4 align-text-bottom" />
                  Save this in a secret manager (Vault, Doppler, K8s Secret). We
                  can&apos;t show it again.
                </div>
                <div className="space-y-1.5">
                  <Label className="text-xs">Token secret</Label>
                  <div className="flex gap-2">
                    <Input
                      readOnly
                      value={reveal.token}
                      className="font-mono text-xs"
                      onFocus={(e) => e.currentTarget.select()}
                    />
                    <Button variant="outline" onClick={copyToken} type="button">
                      <ClipboardDocumentIcon className="size-4" />
                      Copy
                    </Button>
                  </div>
                  <p className={cn(T.code, "text-muted-foreground")}>
                    prefix: paladin_pat_{reveal.prefix}…
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
                <DialogTitle>New M2M token</DialogTitle>
                <DialogDescription>
                  Issued for service-to-service automation. Plaintext is
                  returned exactly once.
                </DialogDescription>
              </DialogHeader>
              <div className="space-y-4 py-4">
                <div className="space-y-1.5">
                  <Label htmlFor="m2m-name">Name</Label>
                  <Input
                    id="m2m-name"
                    autoFocus
                    placeholder="ci-uploader, terraform-svc, …"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                  />
                  <p className={T.hint}>
                    Operator-facing label. Shown in audit logs.
                  </p>
                </div>

                <div className="space-y-1.5">
                  <Label className="text-xs">Audience (planes)</Label>
                  <div className="flex flex-wrap gap-2">
                    {AUDIENCE_CHOICES.map((aud) => (
                      <label
                        key={aud}
                        className={cn(
                          "flex cursor-pointer items-center gap-1.5 rounded-md border px-2 py-1 text-xs",
                          audience.has(aud)
                            ? "border-primary bg-primary/10"
                            : "border-input",
                        )}
                      >
                        <input
                          type="checkbox"
                          checked={audience.has(aud)}
                          onChange={() => toggleAudience(aud)}
                          className="size-3 accent-primary"
                        />
                        <span className="font-mono">{aud}</span>
                      </label>
                    ))}
                  </div>
                </div>

                <div className="space-y-1.5">
                  <Label htmlFor="m2m-scopes">Scopes (optional)</Label>
                  <Input
                    id="m2m-scopes"
                    placeholder="object:read, bucket:list, …"
                    value={scopes}
                    onChange={(e) => setScopes(e.target.value)}
                  />
                  <p className={T.hint}>
                    Comma- or space-separated. Backend interprets per its Cedar
                    policy mapping.
                  </p>
                </div>

                <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                  <div className="space-y-1.5">
                    <Label htmlFor="m2m-rpm">Rate limit (rpm)</Label>
                    <Input
                      id="m2m-rpm"
                      type="number"
                      min={0}
                      placeholder="0 = unlimited"
                      value={rateLimit}
                      onChange={(e) => setRateLimit(e.target.value)}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="m2m-ttl">Expiration</Label>
                    <Select
                      options={TTL_OPTIONS.map((o) => ({
                        value: o.value,
                        label: o.label,
                      }))}
                      value={ttl}
                      onChange={setTtl}
                      className="w-full"
                    />
                  </div>
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
                  disabled={
                    creating || !name.trim() || audience.size === 0 || !tenantId
                  }
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
                paladin_pat_{revokeTarget?.prefix}…
              </span>{" "}
              will stop accepting traffic immediately. This cannot be undone —
              create a new one to replace.
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
