"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowPathIcon,
  KeyIcon,
  PlusIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { Code, ConnectError } from "@connectrpc/connect";

import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
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
import { useNotification } from "@/components/ui/Notification";
import { useTenant } from "../tenant-context";
import { apiTokenClient } from "@/lib/connect/client";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { APIToken } from "@/gen/paladin/admin/v1/api_token_service_pb";
import { isAbortError } from "@/hooks/errorContract";
import { formatTimestamp, isRevoked, isExpired } from "./_constants";
import { CreateTokenDialog } from "./CreateTokenDialog";
import { RevokeTokenDialog } from "./RevokeTokenDialog";

// Per-token sliding-window usage snapshot keyed by token.id; "never" ⇒
// the token was never verified (GetUsage NotFound).
type UsageSnap = {
  limitRpm: number;
  weighted: number;
  currentBucket: bigint;
  resetsAtMs: number;
};
type UsageMap = Map<string, UsageSnap | "never">;
const EMPTY_USAGE: UsageMap = new Map();

// /m2m-tokens — service-to-service hashed-bearer tokens.
//
// The surviving M2M token surface: admin/v1.APITokenService tokens (the
// former user-scoped iam.ApiKey / ApiKeyService was removed and unified
// onto this one). These follow the GitHub PAT /
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

export default function M2MTokensPage() {
  // tenantId comes from the URL (TenantLayout) so platform-admins
  // can issue tokens for any tenant by navigating in. Legacy /m2m-
  // tokens used useScope() (auth-bound), which limited the page
  // to the signed-in tenant.
  const tenant = useTenant();
  const tenantId = tenant.tenantId;
  const { showNotification } = useNotification();

  // ── list state ──────────────────────────────────────────────────────
  const [includeRevoked, setIncludeRevoked] = useState(false);
  const [includeExpired, setIncludeExpired] = useState(false);

  const tokensQuery = useQuery({
    queryKey: ["m2mTokens", tenantId, includeRevoked, includeExpired],
    enabled: !!tenantId,
    retry: false, // queryFn toasts real failures.
    queryFn: async ({ signal }) => {
      try {
        const res = await apiTokenClient.list(
          {
            parent: `tenants/${tenantId}`,
            includeRevoked,
            includeExpired,
            pageSize: 100,
          },
          { signal },
        );
        // Fan-out usage fetches for non-revoked tokens (revoked have no live
        // counters worth showing). NotFound ⇒ "never". The shared signal
        // cancels them if the list query is superseded.
        const entries = await Promise.all(
          res.apiTokens.map(async (t) => {
            if (isRevoked(t)) return null;
            try {
              const u = await apiTokenClient.getUsage(
                { name: t.name },
                { signal },
              );
              return [
                t.id,
                {
                  limitRpm: u.limitRpm,
                  weighted: u.weightedCount,
                  currentBucket: u.currentBucketCount,
                  resetsAtMs: u.windowResetsAt
                    ? Number(u.windowResetsAt.seconds) * 1000
                    : 0,
                },
              ] as const;
            } catch (err) {
              // An aborted query is not a failure the operator needs to see:
              // TanStack cancels in-flight reads on unmount and on supersede.
              if (isAbortError(err)) throw err;
              if (err instanceof ConnectError && err.code === Code.NotFound) {
                return [t.id, "never" as const] as const;
              }
              return null;
            }
          }),
        );
        const usage: UsageMap = new Map(
          entries.filter(
            (e): e is readonly [string, UsageSnap | "never"] => e !== null,
          ),
        );
        return { tokens: res.apiTokens, usage };
      } catch (err) {
        showNotification({
          type: "error",
          title: "Load failed",
          message:
            err instanceof ConnectError
              ? err.rawMessage
              : "Failed to list M2M tokens",
        });
        throw err;
      }
    },
  });
  const tokens = useMemo(
    () => tokensQuery.data?.tokens ?? [],
    [tokensQuery.data],
  );
  const usage = tokensQuery.data?.usage ?? EMPTY_USAGE;
  const loading = tokensQuery.isFetching;
  const fetchTokens = () => tokensQuery.refetch();

  // ── create / revoke dialog targets ──────────────────────────────────
  // The dialogs own their own form + RPC; the page only tracks open state.
  const [createOpen, setCreateOpen] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<APIToken | null>(null);

  const visibleTokens = useMemo(() => tokens, [tokens]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">M2M Tokens</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            Service-to-service hashed-bearer tokens for{" "}
            <span className="font-mono">{tenant.displayName}</span>.
            Audience-pinned and optionally rate-limited.
          </p>
        </div>
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
      </div>

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
                      {t.displayName || (
                        <span className="italic text-muted-foreground">
                          (unnamed)
                        </span>
                      )}
                      {t.scopes.length > 0 ? (
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
                      ) : (
                        <div className={cn("mt-1", T.hint)}>tenant-wide</div>
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
                          aria-label={`Revoke ${t.displayName || t.prefix}`}
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
      <CreateTokenDialog
        open={createOpen}
        tenantId={tenantId}
        onClose={() => {
          setCreateOpen(false);
          void fetchTokens();
        }}
        onCreated={() => void fetchTokens()}
      />

      {/* ─── Revoke confirm ────────────────────────────────────────── */}
      <RevokeTokenDialog
        token={revokeTarget}
        onClose={() => setRevokeTarget(null)}
        onRevoked={() => void fetchTokens()}
      />
    </div>
  );
}
