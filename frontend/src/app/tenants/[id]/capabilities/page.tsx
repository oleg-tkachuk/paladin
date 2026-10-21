"use client";

import { usePageAction } from "@/context/ActionsContext";
import { CREATE_ACTION } from "@/lib/paletteActions";
import { HelpTip } from "@/components/ui/HelpTip";
import { useTenantChangesBlocked } from "../tenant-context";
import { useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import {
  ArrowPathIcon,
  EllipsisVerticalIcon,
  EyeIcon,
  KeyIcon,
  PlusIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { ConnectError, Code } from "@connectrpc/connect";

import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Dropdown } from "@/components/ui/Dropdown";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Select } from "@/components/ui/Select";
import { useNotification } from "@/components/ui/Notification";
import { useTenant } from "../tenant-context";
import { IssueCapabilityDialog } from "./IssueCapabilityDialog";
import { CapabilityDetailsPanel } from "./CapabilityDetailsPanel";
import { useDetailPanel } from "@/hooks/useDetailPanel";
import { openableRow } from "@/components/ui/RecordDialog";
import { RevokeCapabilityDialog } from "./RevokeCapabilityDialog";
import { RevokeBiscuitCopyDialog } from "./RevokeBiscuitCopyDialog";
import { BiscuitCopyUsageDialog } from "./BiscuitCopyUsageDialog";
import {
  PRINCIPAL_KIND_FILTER_OPTIONS,
  SPEND_DIGITS,
  principalKindLabel,
} from "./_constants";
import {
  DEFAULT_STATUSES,
  STATUS_FILTERS,
  capabilityStatus,
  toggleStatus,
} from "./_disposition";
import { requestLimit } from "./_ceilings";
import { budgetLimit, budgetUnit } from "@/lib/format/budgetCeiling";
import { capabilityClient } from "@/lib/connect/client";
import { notFoundIsAnswer } from "@/lib/connect/expected";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import {
  CapabilityDisposition,
  PrincipalKind,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import { ABSTRACT_UNIT_CODE, formatMoney } from "@/lib/format/money";
import type { Money } from "@/gen/google/type/money_pb";
import { isAbortError, errorMessage } from "@/hooks/errorContract";
import { useDebouncedValue } from "@/hooks/useDebouncedValue";
import { Timestamp } from "@/components/Timestamp";
import { ListLoadError } from "@/components/ui/ListLoadError";

// Per-capability usage snapshot keyed by capability id; "never" ⇒ the
// capability has no usage row yet (GetUsage NotFound).
type UsageSnap = {
  requestCount: bigint;
  // Absent on a usage row that has spent nothing in any unit.
  spent: Money | undefined;
};
type UsageMap = Map<string, UsageSnap | "never">;
const EMPTY_USAGE: UsageMap = new Map();

// Optional columns appear by the width of the table's card, not the viewport:
// the sidebar takes a fixed share of the window, so a viewport breakpoint
// showed every column on a card too narrow for them and pushed Status and the
// actions menu past its right edge. Least useful column goes first; below @xl
// the ID itself truncates to a prefix, with the full id in its tooltip.
const COL_AUDIENCE = "hidden @2xl:table-cell";
const COL_EXPIRES = "hidden @3xl:table-cell";
const COL_USAGE = "hidden @4xl:table-cell";
const COL_ISSUED = "hidden @6xl:table-cell";
// Every header column, hidden ones included: a full-width row must span them all.
const COLUMN_COUNT = 9;

/** Capabilities per page of the list; Load more reads the next. */
const CAPABILITIES_PAGE_SIZE = 100;

// /capabilities — agent-runtime authorisation primitive.
//
// A capability is a short-lived, signed JWT that delegates a narrow
// slice of the operator's authority to an agent / service. Caveats
// pin the principal, allowed ops, resource scope, expiry, optional
// budget. The CapabilityService Issue/List/Revoke surface lives on
// the admin plane.
//
// This page is the operator surface:
//
//   • List — the tenant's capabilities, newest first, the active ones
//     until the filter asks for expired or revoked too; a principal
//     kind and a subject narrow it.
//   • Issue   — opens a dialog that builds CapabilityServiceIssue
//     Request {subject, audience, caveats, ttl}. The compact JWT is
//     returned exactly once and shown in a "save now" reveal panel.
//   • Revoke — single-row action; cascade-children flag is exposed
//     under "Advanced" for revoking a delegation tree.
//   • Revoke a copy — a header action: the operator pastes one copy of
//     a capability's Biscuit, which no list holds, and only that copy
//     and the copies narrowed from it stop working.
//   • Copy usage — a header action beside it: the limits narrowed onto
//     a pasted copy, and what has been used against each.
//
// Delegate is intentionally NOT exposed in the operator UI. Delegation
// is an agent-side flow (the agent narrows its own capability when
// spawning a sub-agent); building a Delegate form from the admin
// console invites copy-pasting parent capability ids and producing
// confusing trees. If/when an explicit delegation surface is needed
// we can add a per-row "Delegate" action.

export default function CapabilitiesPage() {
  const changesBlocked = useTenantChangesBlocked();
  // tenantId comes from the URL (TenantLayout). Legacy /capabilities
  // pulled it from useScope() so the page only listed caps issued
  // under the signed-in tenant; the new path lets platform-admins
  // browse + issue + revoke for any tenant by navigating in.
  const tenant = useTenant();
  const tenantId = tenant.tenantId;
  const { showNotification } = useNotification();

  // ── filters ─────────────────────────────────────────────────────────
  // List takes the tenant alone; the kind, the subject and the statuses
  // narrow it. Each is part of the query key, so changing one lists again.
  const [principalKind, setPrincipalKind] = useState<string>(
    String(PrincipalKind.UNSPECIFIED),
  );
  const [subject, setSubject] = useState("");
  // The subject lists once typing settles, not on each keystroke.
  const subjectFilter = useDebouncedValue(subject.trim());
  const [statuses, setStatuses] =
    useState<readonly CapabilityDisposition[]>(DEFAULT_STATUSES);

  const browseQuery = useInfiniteQuery({
    queryKey: [
      "capabilities",
      tenantId,
      principalKind,
      subjectFilter,
      statuses,
    ],
    enabled: !!tenantId,
    retry: false, // queryFn toasts real failures.
    initialPageParam: "",
    queryFn: async ({ pageParam, signal }) => {
      try {
        const page = await capabilityClient.list(
          {
            tenantId,
            principalKind: Number(principalKind),
            subject: subjectFilter,
            dispositions: [...statuses],
            sortOrder: SortOrder.DESC,
            pageSize: CAPABILITIES_PAGE_SIZE,
            pageToken: pageParam,
          },
          { signal },
        );
        const capabilities = page.capabilities;
        // Fan-out usage fetches in parallel — each is a single index hit, so
        // a page in flight is fine. NotFound ⇒ "never" sentinel. The shared
        // signal cancels them all if the list is superseded.
        const entries = await Promise.all(
          capabilities.map(async (c) => {
            try {
              const u = await capabilityClient.getUsage(
                { id: c.id },
                notFoundIsAnswer({ signal }),
              );
              return [
                c.id,
                {
                  requestCount: u.requestCount,
                  spent: u.spent,
                },
              ] as const;
            } catch (err) {
              // An aborted query is not a failure the operator needs to see:
              // TanStack cancels in-flight reads on unmount and on supersede.
              if (isAbortError(err)) throw err;
              if (err instanceof ConnectError && err.code === Code.NotFound) {
                return [c.id, "never" as const] as const;
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
        return { items: capabilities, usage, next: page.nextPageToken };
      } catch (err) {
        // A list superseded by a newer filter is cancelled, not failed.
        if (!isAbortError(err)) {
          showNotification({
            type: "error",
            title: "Load failed",
            message: errorMessage(err, "Failed to list capabilities"),
          });
        }
        throw err;
      }
    },
    getNextPageParam: (last) => last.next || undefined,
  });
  const items = useMemo(
    () => browseQuery.data?.pages.flatMap((p) => p.items) ?? [],
    [browseQuery.data],
  );
  const usage = useMemo(
    () =>
      browseQuery.data
        ? new Map(browseQuery.data.pages.flatMap((p) => [...p.usage]))
        : EMPTY_USAGE,
    [browseQuery.data],
  );
  const loading = browseQuery.isFetching;
  const fetchList = async () => {
    await browseQuery.refetch();
  };

  // ── issue dialog ────────────────────────────────────────────────────
  const [createOpen, setCreateOpen] = useState(false);
  // The palette offers the create while this page does.
  usePageAction(
    !changesBlocked
      ? {
          id: CREATE_ACTION.capability,
          label: "Issue capability",
          description: "A capability for an agent or a user of this tenant",
          icon: PlusIcon,
          category: "create",
          perform: () => setCreateOpen(true),
        }
      : null,
  );
  // The Issue dialog owns its form + reveal state. The page keeps only the
  // open flag (above) + the two callbacks the dialog needs:

  // onIssued — show the just-issued capability: the filters move to its
  // principal and take in the active ones, so it is on the list, newest
  // first, when the reveal panel closes.
  const handleIssued = (
    _capability: Capability,
    kind: string,
    subj: string,
  ) => {
    setPrincipalKind(kind);
    setSubject(subj);
    setStatuses((s) => toggleStatus(s, CapabilityDisposition.ACTIVE, true));
  };

  // onClose — close the dialog and refetch so the list reflects the server
  // (the seeded single row is replaced by the authoritative List response).
  const handleIssueClose = () => {
    setCreateOpen(false);
    void fetchList();
  };

  // ── details ─────────────────────────────────────────────────────────
  // Read-only "everything we know about this capability" panel, opened
  // from a row or its kebab menu, beside the list. Mirrors what the List
  // response already carries plus the usage snapshot we've already
  // fetched, and the record for who issued it and why it was revoked.
  // Token plaintext is NOT shown (the issuer keeps no copy by design; only
  // the freshly-issued reveal panel sees it). The capability open in it is
  // in the URL (?id=), so the audit log can link to one and an open one's
  // address is its link.
  const linked = useDetailPanel();

  // ── revoke ──────────────────────────────────────────────────────────
  const [revokeTarget, setRevokeTarget] = useState<Capability | null>(null);
  const [revokeCopyOpen, setRevokeCopyOpen] = useState(false);
  const [copyUsageOpen, setCopyUsageOpen] = useState(false);

  const visibleItems = useMemo(() => items, [items]);
  // The audit log links by id alone; the dialog reads a capability the
  // browsed list does not hold from its record.
  const listedTarget = items.find((c) => c.id === linked.openId) ?? null;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="flex items-center gap-2 text-lg font-semibold">
            Capabilities <HelpTip topic="capability" />
          </h2>
          <p className={cn(T.helper, "max-w-prose")}>
            Short-lived, signed authorisation tokens for agent / service
            runtimes acting on behalf of{" "}
            <span className="font-mono">{tenant.displayName}</span>.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => void fetchList()}
            disabled={loading}
          >
            <ArrowPathIcon
              className={cn("size-4", loading && "animate-spin")}
            />
            Refresh
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setCopyUsageOpen(true)}
          >
            <EyeIcon className="size-4" />
            Copy usage
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setRevokeCopyOpen(true)}
          >
            <TrashIcon className="size-4" />
            Revoke a copy
          </Button>
          <Button
            size="sm"
            onClick={() => setCreateOpen(true)}
            disabled={Boolean(changesBlocked)}
            title={changesBlocked ?? undefined}
          >
            <PlusIcon className="size-4" />
            Issue capability
          </Button>
        </div>
      </div>

      {/* ─── Filters ───────────────────────────────────────────────── */}
      <Card className="p-4">
        <div className="grid grid-cols-1 gap-3 md:grid-cols-[180px_1fr_auto]">
          <div className="space-y-1.5">
            <Label className="text-xs">Principal kind</Label>
            <Select
              aria-label="Principal kind"
              options={PRINCIPAL_KIND_FILTER_OPTIONS}
              value={principalKind}
              onChange={setPrincipalKind}
              className="w-full"
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs" htmlFor="cap-subj">
              Subject
            </Label>
            <Input
              id="cap-subj"
              placeholder="agent-id, user subject, service-account name…"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
            />
          </div>
          <fieldset className="flex items-end gap-3 text-sm">
            <legend className="sr-only">Status</legend>
            {STATUS_FILTERS.map((f) => {
              const checked = statuses.includes(f.disposition);
              return (
                <label
                  key={f.disposition}
                  className="flex cursor-pointer items-center gap-2"
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    // The last status stays on: none would list the default.
                    disabled={checked && statuses.length === 1}
                    onChange={(e) =>
                      setStatuses((s) =>
                        toggleStatus(s, f.disposition, e.target.checked),
                      )
                    }
                    className="size-4 accent-primary"
                  />
                  <span className="text-muted-foreground">{f.label}</span>
                </label>
              );
            })}
          </fieldset>
        </div>
      </Card>

      {/* ─── Result table ────────────────────────────────────────────── */}
      <Card className="@container p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="@xl:w-70">ID</TableHead>
              <TableHead>Principal</TableHead>
              <TableHead>Caveats</TableHead>
              <TableHead className={COL_AUDIENCE}>Audience</TableHead>
              <TableHead className={COL_ISSUED}>Issued</TableHead>
              <TableHead className={COL_EXPIRES}>Expires</TableHead>
              <TableHead numeric className={COL_USAGE}>
                Usage
              </TableHead>
              <TableHead className="w-25">Status</TableHead>
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && visibleItems.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={COLUMN_COUNT} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : browseQuery.isError && !browseQuery.data ? (
              // "No capabilities for this principal" for a failed list hides live grants.
              <TableRow>
                <TableCell colSpan={COLUMN_COUNT} className="h-40 text-center">
                  <ListLoadError
                    what="Capabilities"
                    reason={errorMessage(browseQuery.error)}
                    onRetry={() => void browseQuery.refetch()}
                  />
                </TableCell>
              </TableRow>
            ) : visibleItems.length === 0 ? (
              <TableRow>
                <TableCell colSpan={COLUMN_COUNT} className="h-40 text-center">
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <KeyIcon className="size-8 opacity-40" />
                    <p className="text-sm">
                      No capabilities match these filters.
                    </p>
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => setCreateOpen(true)}
                      disabled={Boolean(changesBlocked)}
                      title={changesBlocked ?? undefined}
                    >
                      <PlusIcon className="size-4" />
                      Issue one
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              visibleItems.map((c) => {
                const status = capabilityStatus(c);
                const row = openableRow(() => linked.open(c.id));
                return (
                  <TableRow
                    key={c.id}
                    {...row}
                    {...linked.rowProps(c.id)}
                    className={cn("group", row.className)}
                  >
                    <TableCell>
                      <span
                        className="block max-w-24 truncate @xl:max-w-65 font-mono text-xs"
                        title={c.id}
                      >
                        {c.id}
                      </span>
                      {c.parentId && (
                        <span
                          className={cn(
                            T.codeSmall,
                            "block max-w-24 truncate @xl:max-w-65 text-muted-foreground",
                          )}
                          title={`parent: ${c.parentId}`}
                        >
                          ↪ {c.parentId.slice(0, 8)}…
                        </span>
                      )}
                    </TableCell>
                    <TableCell>
                      <span className="block text-xs text-muted-foreground">
                        {principalKindLabel(c.subject?.kind)}
                      </span>
                      <span
                        className="block max-w-32 truncate @xl:max-w-48 font-mono text-xs"
                        title={c.subject?.subject}
                      >
                        {c.subject?.subject || "—"}
                      </span>
                    </TableCell>
                    {/* Caveats wrap: resource prefixes are free text, and a
                        long one on a single line would widen the whole table. */}
                    <TableCell className="whitespace-normal">
                      <div className="flex flex-wrap gap-1">
                        {(c.caveats?.ops ?? []).map((op) => (
                          <Badge
                            key={op}
                            variant="outline"
                            className="font-mono"
                          >
                            {op}
                          </Badge>
                        ))}
                      </div>
                      {(c.caveats?.resourcePrefixes ?? []).length > 0 && (
                        <div
                          className={cn(
                            T.codeSmall,
                            "mt-1 break-all text-muted-foreground",
                          )}
                        >
                          {c.caveats!.resourcePrefixes.join(", ")}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className={COL_AUDIENCE}>
                      <div className="flex flex-wrap gap-1">
                        {c.audience.map((a) => (
                          <Badge key={a} variant="secondary">
                            {a}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell
                      className={cn(
                        COL_ISSUED,
                        T.code,
                        "text-muted-foreground",
                      )}
                    >
                      <Timestamp ts={c.issuedAt} />
                    </TableCell>
                    <TableCell
                      className={cn(
                        COL_EXPIRES,
                        T.code,
                        "text-muted-foreground",
                      )}
                    >
                      <Timestamp ts={c.expiresAt} />
                    </TableCell>
                    <TableCell numeric className={cn(COL_USAGE, T.code)}>
                      {(() => {
                        const u = usage.get(c.id);
                        if (u === undefined)
                          return (
                            <span className="text-muted-foreground">…</span>
                          );
                        if (u === "never")
                          return (
                            <span className="text-muted-foreground">
                              never used
                            </span>
                          );
                        const reqCap = requestLimit(c.caveats);
                        const budgetCap = budgetLimit(c.caveats?.budgetCeiling);
                        // Usage row → caveats → XXX fallback. A "USD"
                        // default would lie about the actual unit when
                        // both are absent (metering-only capabilities).
                        const cellUnit =
                          u.spent?.currencyCode ||
                          budgetUnit(c.caveats?.budgetCeiling) ||
                          ABSTRACT_UNIT_CODE;
                        return (
                          <div className="space-y-0.5">
                            <div>
                              <span className="text-muted-foreground">
                                req{" "}
                              </span>
                              {u.requestCount.toString()}
                              {reqCap !== null && (
                                <span className="text-muted-foreground">
                                  {" "}
                                  / {reqCap}
                                </span>
                              )}
                            </div>
                            <div>
                              {formatMoney(u.spent, {
                                fallbackUnit: cellUnit,
                                fractionDigits: SPEND_DIGITS,
                              })}
                              {budgetCap !== undefined && (
                                <span className="text-muted-foreground">
                                  {" "}
                                  /{" "}
                                  {formatMoney(budgetCap, {
                                    fallbackUnit: cellUnit,
                                  })}
                                </span>
                              )}
                            </div>
                          </div>
                        );
                      })()}
                    </TableCell>
                    <TableCell>
                      <Badge variant={status.variant} className={T.code}>
                        {status.label}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right">
                      {/* Kebab menu — View details + Revoke. The row
                          opens the details too: they open beside the
                          list rather than over it, so an accidental open
                          costs nothing. */}
                      <Dropdown align="right" width="w-44">
                        <Dropdown.Trigger
                          className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                          activeClassName="bg-accent text-foreground"
                        >
                          <span className="sr-only">
                            Actions for capability {c.id}
                          </span>
                          <EllipsisVerticalIcon className="size-4" />
                        </Dropdown.Trigger>
                        <Dropdown.Menu className="py-1">
                          <Dropdown.Item
                            className="p-0"
                            onClick={() => linked.open(c.id)}
                          >
                            <div className="flex w-full items-center gap-2 px-3 py-1.5 text-xs hover:bg-accent">
                              <EyeIcon className="size-4 text-muted-foreground" />
                              View details
                            </div>
                          </Dropdown.Item>
                          <div className="my-1 h-px bg-border" />
                          <Dropdown.Item
                            className="p-0"
                            onClick={() => setRevokeTarget(c)}
                          >
                            <div className="flex w-full items-center gap-2 px-3 py-1.5 text-xs text-destructive hover:bg-destructive/10">
                              <TrashIcon className="size-4" />
                              Revoke
                            </div>
                          </Dropdown.Item>
                        </Dropdown.Menu>
                      </Dropdown>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
        {browseQuery.hasNextPage && (
          <div className="flex justify-center border-t p-3">
            <Button
              variant="outline"
              size="sm"
              onClick={() => void browseQuery.fetchNextPage()}
              disabled={loading}
            >
              Load more
            </Button>
          </div>
        )}
      </Card>

      {/* ─── Issue dialog ────────────────────────────────────────────── */}
      <IssueCapabilityDialog
        open={createOpen}
        tenantId={tenantId}
        onClose={handleIssueClose}
        onIssued={handleIssued}
      />

      {/* ─── Details panel ─────────────────────────────────────────── */}
      <CapabilityDetailsPanel
        id={linked.openId}
        cap={listedTarget}
        usageEntry={linked.openId ? usage.get(linked.openId) : undefined}
        onClose={linked.close}
        onRevoke={setRevokeTarget}
      />

      {/* ─── Revoke confirm ────────────────────────────────────────── */}
      <RevokeCapabilityDialog
        cap={revokeTarget}
        onClose={() => setRevokeTarget(null)}
        onRevoked={() => void fetchList()}
      />

      {/* ─── Revoke one Biscuit copy ───────────────────────────────── */}
      <RevokeBiscuitCopyDialog
        open={revokeCopyOpen}
        onClose={() => setRevokeCopyOpen(false)}
      />

      {/* ─── One Biscuit copy's usage ──────────────────────────────── */}
      <BiscuitCopyUsageDialog
        open={copyUsageOpen}
        onClose={() => setCopyUsageOpen(false)}
      />
    </div>
  );
}
