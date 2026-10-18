"use client";

import { listAllPages } from "@/lib/listAllPages";
import { API_PAGE_SIZE_MAX } from "@/constants";
import { useTenantChangesBlocked } from "../tenant-context";
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
import { SortableHead } from "@/components/ui/SortHeader";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useTableView } from "@/hooks/useTableView";
import {
  DEFAULT_LIFECYCLE_FILTER,
  LIFECYCLE_BADGE,
  LIFECYCLE_FILTERS,
  LIFECYCLE_FILTER_LABEL,
  keepsLifecycle,
  type LifecycleFilter,
} from "@/lib/table/lifecycle";
import type { SortAccessors } from "@/lib/table/view";
import { useNotification } from "@/components/ui/Notification";
import { useTenant } from "../tenant-context";
import { apiTokenClient } from "@/lib/connect/client";
import { notFoundIsAnswer } from "@/lib/connect/expected";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { APIToken } from "@/gen/paladin/admin/v1/api_token_service_pb";
import { isAbortError, errorMessage } from "@/hooks/errorContract";
import { isRevoked, listFlagsFor, tokenLifecycle } from "./_constants";
import { CreateTokenDialog } from "./CreateTokenDialog";
import { RevokeTokenDialog } from "./RevokeTokenDialog";
import {
  TokenRecordDialog,
  TokenStatus,
  TokenUsage,
  type UsageSnap,
} from "./TokenRecordDialog";
import { openableRow } from "@/components/ui/RecordDialog";
import { Timestamp } from "@/components/Timestamp";
import { ListLoadError } from "@/components/ui/ListLoadError";

// Optional columns appear by the width of the table's card (see Table):
// least useful goes first, so Status and the actions menu always fit.
const COL_PREFIX = "hidden @md:table-cell";
const COL_AUDIENCE = "hidden @2xl:table-cell";
const COL_EXPIRES = "hidden @3xl:table-cell";
const COL_RATE = "hidden @4xl:table-cell";
const COL_LAST_USED = "hidden @5xl:table-cell";
const COL_USAGE = "hidden @6xl:table-cell";

type UsageMap = Map<string, UsageSnap | "never">;
const EMPTY_USAGE: UsageMap = new Map();

// The columns of the table; the placeholder rows span them.
const TOKEN_COLUMNS = 9;

type TokenColumn =
  "prefix" | "name" | "audience" | "rate" | "used" | "expires" | "status";
const at = (t: APIToken["expiresAt"]) => (t ? timestampDate(t) : undefined);
const TOKEN_SORT: SortAccessors<APIToken, TokenColumn> = {
  prefix: (t) => t.prefix,
  name: (t) => t.displayName || undefined,
  audience: (t) => t.audience.join(" ") || undefined,
  // An unlimited token sorts as the most generous.
  rate: (t) =>
    t.rateLimit.case === "rateLimitRpm"
      ? t.rateLimit.value
      : Number.POSITIVE_INFINITY,
  used: (t) => at(t.lastUsedAt),
  expires: (t) => at(t.expiresAt),
  status: (t) => LIFECYCLE_BADGE[tokenLifecycle(t)].label,
};
const TOKEN_SEARCH = (t: APIToken) => [
  t.displayName,
  t.prefix,
  t.audience.join(" "),
  t.scopes.join(" "),
  t.roles.join(" "),
  LIFECYCLE_BADGE[tokenLifecycle(t)].label,
];

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
  const changesBlocked = useTenantChangesBlocked();
  // tenantId comes from the URL (TenantLayout) so platform-admins
  // can issue tokens for any tenant by navigating in. Legacy /m2m-
  // tokens used useScope() (auth-bound), which limited the page
  // to the signed-in tenant.
  const tenant = useTenant();
  const tenantId = tenant.tenantId;
  const { showNotification } = useNotification();

  // ── list state ──────────────────────────────────────────────────────
  const [stateFilter, setStateFilter] = useState<LifecycleFilter>(
    DEFAULT_LIFECYCLE_FILTER,
  );
  const { includeRevoked, includeExpired } = listFlagsFor(stateFilter);

  const tokensQuery = useQuery({
    queryKey: ["m2mTokens", tenantId, includeRevoked, includeExpired],
    enabled: !!tenantId,
    retry: false, // queryFn toasts real failures.
    queryFn: async ({ signal }) => {
      try {
        // Every page: a token past the first would be invisible here, and
        // so could not be revoked from the page that lists them.
        const apiTokens = await listAllPages((pageToken) =>
          apiTokenClient
            .list(
              {
                parent: `tenants/${tenantId}`,
                includeRevoked,
                includeExpired,
                pageSize: API_PAGE_SIZE_MAX,
                pageToken,
              },
              { signal },
            )
            .then((r) => ({ items: r.apiTokens, next: r.nextPageToken })),
        );
        // Fan-out usage fetches for non-revoked tokens (revoked have no live
        // counters worth showing). NotFound ⇒ "never". The shared signal
        // cancels them if the list query is superseded.
        const entries = await Promise.all(
          apiTokens.map(async (t) => {
            if (isRevoked(t)) return null;
            try {
              const u = await apiTokenClient.getUsage(
                { name: t.name },
                notFoundIsAnswer({ signal }),
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
        return { tokens: apiTokens, usage };
      } catch (err) {
        showNotification({
          type: "error",
          title: "Load failed",
          message: errorMessage(err, "Failed to list M2M tokens"),
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
  // When the usage was read: a window's reset counts down from then.
  const readAtMs = tokensQuery.dataUpdatedAt;
  const loading = tokensQuery.isFetching;
  const fetchTokens = () => tokensQuery.refetch();

  // ── create / revoke dialog targets ──────────────────────────────────
  // The dialogs own their own form + RPC; the page only tracks open state.
  const [createOpen, setCreateOpen] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<APIToken | null>(null);
  const [opened, setOpened] = useState<APIToken | null>(null);

  const inState = useMemo(
    () => tokens.filter((t) => keepsLifecycle(stateFilter, tokenLifecycle(t))),
    [tokens, stateFilter],
  );
  const view = useTableView(inState, {
    text: TOKEN_SEARCH,
    accessors: TOKEN_SORT,
  });
  const visibleTokens = view.rows;
  const sortProps = { current: view.sort, onSort: view.toggleSort };

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
          <Button
            size="sm"
            onClick={() => setCreateOpen(true)}
            disabled={Boolean(changesBlocked)}
            title={changesBlocked ?? undefined}
          >
            <PlusIcon className="size-4" />
            New token
          </Button>
        </div>
      </div>

      <TableToolbar
        query={view.query}
        onQueryChange={view.setQuery}
        searchLabel="Search M2M tokens"
        placeholder="Search by name, prefix, audience, role or scope…"
        filter={{
          label: LIFECYCLE_FILTER_LABEL,
          options: LIFECYCLE_FILTERS,
          value: stateFilter,
          onChange: setStateFilter,
        }}
        count={
          tokensQuery.data
            ? { shown: visibleTokens.length, total: view.total }
            : undefined
        }
      />

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <SortableHead
                label="Prefix"
                column="prefix"
                className={cn(COL_PREFIX, "w-35")}
                {...sortProps}
              />
              <SortableHead label="Name" column="name" {...sortProps} />
              <SortableHead
                label="Audience"
                column="audience"
                className={COL_AUDIENCE}
                {...sortProps}
              />
              <SortableHead
                label="Rate (rpm)"
                column="rate"
                className={COL_RATE}
                {...sortProps}
              />
              <TableHead className={cn(COL_USAGE, "w-35")}>
                Usage (1m)
              </TableHead>
              <SortableHead
                label="Last used"
                column="used"
                className={COL_LAST_USED}
                {...sortProps}
              />
              <SortableHead
                label="Expires"
                column="expires"
                className={COL_EXPIRES}
                {...sortProps}
              />
              <SortableHead
                label="Status"
                column="status"
                className="w-25"
                {...sortProps}
              />
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && visibleTokens.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={TOKEN_COLUMNS} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : tokensQuery.isError && !tokensQuery.data ? (
              // "No M2M tokens yet" for a failed list invites creating one that may exist.
              <TableRow>
                <TableCell colSpan={TOKEN_COLUMNS} className="h-40 text-center">
                  <ListLoadError
                    what="M2M tokens"
                    reason={errorMessage(tokensQuery.error)}
                    onRetry={() => void tokensQuery.refetch()}
                  />
                </TableCell>
              </TableRow>
            ) : visibleTokens.length === 0 && view.total > 0 ? (
              <TableRow>
                <TableCell
                  colSpan={TOKEN_COLUMNS}
                  className="py-8 text-center text-muted-foreground"
                >
                  No token matches the search.
                </TableCell>
              </TableRow>
            ) : visibleTokens.length === 0 &&
              stateFilter !== DEFAULT_LIFECYCLE_FILTER ? (
              <TableRow>
                <TableCell
                  colSpan={TOKEN_COLUMNS}
                  className="py-8 text-center text-muted-foreground"
                >
                  No token is in this state.
                </TableCell>
              </TableRow>
            ) : visibleTokens.length === 0 ? (
              <TableRow>
                <TableCell colSpan={TOKEN_COLUMNS} className="h-40 text-center">
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <KeyIcon className="size-8 opacity-40" />
                    <p className="text-sm">No M2M tokens yet.</p>
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => setCreateOpen(true)}
                      disabled={Boolean(changesBlocked)}
                      title={changesBlocked ?? undefined}
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
                const row = openableRow(() => setOpened(t));
                return (
                  <TableRow
                    key={t.id}
                    {...row}
                    className={cn(row.className, "group")}
                  >
                    <TableCell className={COL_PREFIX}>
                      <span className="font-mono text-xs">
                        paladin_pat_{t.prefix}…
                      </span>
                    </TableCell>
                    <TableCell className="font-medium whitespace-normal break-words">
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
                              className="font-mono"
                            >
                              {s}
                            </Badge>
                          ))}
                        </div>
                      ) : (
                        <div className={cn("mt-1", T.hint)}>tenant-wide</div>
                      )}
                    </TableCell>
                    <TableCell className={COL_AUDIENCE}>
                      <div className="flex flex-wrap gap-1">
                        {t.audience.map((a) => (
                          <Badge key={a} variant="secondary">
                            {a}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell
                      className={cn(COL_RATE, T.code, "text-muted-foreground")}
                    >
                      {t.rateLimit.case === "rateLimitRpm"
                        ? t.rateLimit.value
                        : "∞"}
                    </TableCell>
                    <TableCell className={cn(COL_USAGE, T.code)}>
                      <TokenUsage
                        revoked={revoked}
                        usage={usage.get(t.id)}
                        readAtMs={readAtMs}
                      />
                    </TableCell>
                    <TableCell
                      className={cn(
                        COL_LAST_USED,
                        T.code,
                        "text-muted-foreground",
                      )}
                    >
                      {t.lastUsedAt ? <Timestamp ts={t.lastUsedAt} /> : "never"}
                    </TableCell>
                    <TableCell
                      className={cn(
                        COL_EXPIRES,
                        T.code,
                        "text-muted-foreground",
                      )}
                    >
                      {t.expiresAt ? <Timestamp ts={t.expiresAt} /> : "never"}
                    </TableCell>
                    <TableCell>
                      <TokenStatus token={t} />
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

      <TokenRecordDialog
        token={opened}
        usage={opened ? usage.get(opened.id) : undefined}
        readAtMs={readAtMs}
        onClose={() => setOpened(null)}
      />

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
