"use client";

import { usePageAction } from "@/context/ActionsContext";
import { CREATE_ACTION } from "@/lib/paletteActions";
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

import { ListBody, ListPage } from "@/components/layout/templates/ListPage";
import { emptyReason } from "@/lib/emptyState";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { SortableHead, StaticHead } from "@/components/ui/SortHeader";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useTableView } from "@/hooks/useTableView";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { useTableFacets } from "@/hooks/useTableFacets";
import { useTablePage } from "@/hooks/useTablePage";
import { TablePagination } from "@/components/ui/TablePagination";
import { LIFECYCLE_BADGE, lifecycleFacet } from "@/lib/table/lifecycle";
import type { SortAccessors } from "@/lib/table/view";
import { useNotification } from "@/components/ui/Notification";
import { useTenant } from "../tenant-context";
import { apiTokenClient } from "@/lib/connect/client";
import { notFoundIsAnswer } from "@/lib/connect/expected";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { APIToken } from "@/gen/paladin/admin/v1/api_token_service_pb";
import { isAbortError, errorMessage } from "@/hooks/errorContract";
import { isRevoked, tokenLifecycle } from "./_constants";
import { CreateTokenDialog } from "./CreateTokenDialog";
import { RevokeTokenDialog } from "./RevokeTokenDialog";
import {
  TokenRecordPanel,
  TokenStatus,
  TokenUsage,
  type UsageSnap,
} from "./TokenRecordPanel";
import { openableRow } from "@/components/ui/RecordDialog";
import { useDetailPanel } from "@/hooks/useDetailPanel";
import { Timestamp } from "@/components/Timestamp";
import { TableScope } from "@/components/ui/TableScope";

/** This list's id in the user's table preferences. */
const TABLE_ID = "m2mTokens";

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
const TOKEN_FACETS = [lifecycleFacet(tokenLifecycle)];
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
  // Every token comes, ended ones too: the state facet counts each state
  // over the loaded set, and a count of the live ones alone would say a
  // tenant has no revoked tokens when it was simply not asked. A tenant's
  // tokens are few enough to read whole.
  const tokensQuery = useQuery({
    queryKey: ["m2mTokens", tenantId],
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
                includeRevoked: true,
                includeExpired: true,
                pageSize: API_PAGE_SIZE_MAX,
                pageToken,
              },
              { signal },
            )
            .then((r) => ({ items: r.apiTokens, next: r.nextPageToken })),
        );
        // Fan-out usage fetches for the live tokens: a revoked or expired one
        // verifies no more, so it has no live counters worth showing.
        // NotFound ⇒ "never". The shared signal cancels them if the list
        // query is superseded.
        const entries = await Promise.all(
          apiTokens.map(async (t) => {
            if (tokenLifecycle(t) !== "active") return null;
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
  // The palette offers the create while this page does.
  usePageAction(
    !changesBlocked
      ? {
          id: CREATE_ACTION.m2mToken,
          label: "New token",
          description: "A machine-to-machine token of this tenant",
          icon: PlusIcon,
          category: "create",
          perform: () => setCreateOpen(true),
        }
      : null,
  );
  const [revokeTarget, setRevokeTarget] = useState<APIToken | null>(null);
  // The open token is in the URL, so its panel is a link and survives a
  // reload; it opens beside the list.
  const detail = useDetailPanel();
  const opened = tokens.find((t) => t.id === detail.openId) ?? null;

  const facets = useTableFacets(tokens, TOKEN_FACETS, PAGE_TABLE);
  const view = useTableView(facets.rows, {
    text: TOKEN_SEARCH,
    accessors: TOKEN_SORT,
    url: PAGE_TABLE,
    tableId: TABLE_ID,
  });
  const visibleTokens = view.rows;
  const paged = useTablePage(visibleTokens, {
    url: PAGE_TABLE,
    tableId: TABLE_ID,
    resetOn: [view.query, facets.selection],
  });
  const sortProps = { current: view.sort, onSort: view.toggleSort };

  return (
    <TableScope id={TABLE_ID} sort={view.sort}>
      <ListPage
        footer={<TablePagination {...paged.pagination} />}
        header={
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
        }
        toolbar={
          <TableToolbar
            query={view.query}
            onQueryChange={view.setQuery}
            searchLabel="Search M2M tokens"
            placeholder="Search by name, prefix, audience, role or scope…"
            facets={facets.controls}
            count={
              tokensQuery.data
                ? { shown: visibleTokens.length, total: tokens.length }
                : undefined
            }
          />
        }
      >
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
              <StaticHead className={cn(COL_USAGE, "w-35")}>
                Usage (1m)
              </StaticHead>
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
          <ListBody
            colSpan={TOKEN_COLUMNS}
            loading={loading && visibleTokens.length === 0}
            // "No M2M tokens yet" for a failed list invites creating one that may exist.
            error={
              tokensQuery.isError && !tokensQuery.data
                ? {
                    what: "M2M tokens",
                    reason: errorMessage(tokensQuery.error),
                    onRetry: () => void tokensQuery.refetch(),
                  }
                : null
            }
            empty={
              visibleTokens.length === 0
                ? {
                    icon: KeyIcon,
                    what: "M2M tokens",
                    // A search or a state filter can hide every token.
                    reason: emptyReason({ filtered: tokens.length > 0 }),
                    onClearFilters: () => {
                      view.setQuery("");
                      facets.clear();
                    },
                    purpose:
                      "An M2M token lets a service call Paladin's APIs as this tenant, with the roles and scopes it was issued.",
                    action: {
                      label: "Create the first token",
                      onClick: () => setCreateOpen(true),
                      disabled: Boolean(changesBlocked),
                      title: changesBlocked ?? undefined,
                    },
                  }
                : null
            }
          >
            {paged.rows.map((t) => {
              const revoked = isRevoked(t);
              const row = openableRow(() => detail.open(t.id));
              return (
                <TableRow
                  key={t.id}
                  {...row}
                  {...detail.rowProps(t.id)}
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
                    className={cn(COL_EXPIRES, T.code, "text-muted-foreground")}
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
            })}
          </ListBody>
        </Table>
      </ListPage>

      <TokenRecordPanel
        token={opened}
        usage={opened ? usage.get(opened.id) : undefined}
        readAtMs={readAtMs}
        onClose={detail.close}
        onRevoke={setRevokeTarget}
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
    </TableScope>
  );
}
