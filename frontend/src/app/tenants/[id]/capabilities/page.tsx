"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowPathIcon,
  EllipsisVerticalIcon,
  EyeIcon,
  KeyIcon,
  PlusIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { ConnectError, Code } from "@connectrpc/connect";

import { z } from "zod";
import { safeParseJson } from "@/lib/parseJson";
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
import { CapabilityDetailsDialog } from "./CapabilityDetailsDialog";
import { RevokeCapabilityDialog } from "./RevokeCapabilityDialog";
import { RevokeBiscuitCopyDialog } from "./RevokeBiscuitCopyDialog";
import { BiscuitCopyUsageDialog } from "./BiscuitCopyUsageDialog";
import { PRINCIPAL_KIND_OPTIONS, isExpired } from "./_constants";
import { capabilityClient } from "@/lib/connect/client";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import { PrincipalKind } from "@/gen/paladin/admin/v1/capability_service_pb";
import { formatMoney, fromMicros } from "@/lib/format/money";
import { isAbortError, errorMessage } from "@/hooks/errorContract";
import { formatTimestampUTC } from "@/lib/format/timestamp";
import { ListLoadError } from "@/components/ui/ListLoadError";

// Per-capability usage snapshot keyed by capability id; "never" ⇒ the
// capability has no usage row yet (GetUsage NotFound).
type UsageSnap = {
  requestCount: bigint;
  spentAmount: number;
  unitCode: string;
};
type UsageMap = Map<string, UsageSnap | "never">;
const EMPTY_USAGE: UsageMap = new Map();

// Tenant-scoped "last browsed principal", persisted across reloads.
function readLastBrowse(
  key: string,
): { kind?: string; subject?: string } | null {
  if (typeof window === "undefined") return null;
  try {
    return safeParseJson(
      z.object({ kind: z.string().optional(), subject: z.string().optional() }),
      window.localStorage.getItem(key),
    );
  } catch {
    return null;
  }
}

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
//   • Browse — pick (principal_kind, subject) and List enumerates
//     the capabilities issued to that principal. The List RPC is
//     principal-scoped, not tenant-wide, by design — capabilities
//     of an unknown agent are never enumerable in bulk.
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
  // tenantId comes from the URL (TenantLayout). Legacy /capabilities
  // pulled it from useScope() so the page only listed caps issued
  // under the signed-in tenant; the new path lets platform-admins
  // browse + issue + revoke for any tenant by navigating in.
  const tenant = useTenant();
  const tenantId = tenant.tenantId;
  const { showNotification } = useNotification();

  // ── browse filters ──────────────────────────────────────────────────
  // CapabilityService.List is principal-scoped at the SQL level —
  // (tenant_id, principal_kind, principal_subject) is the lookup key.
  // The protocol has no "show all caps in this tenant" path. So when
  // an operator navigates back to /capabilities they need the
  // browse filter pre-filled with whatever they last looked at,
  // otherwise the page renders empty and reads as "the capabilities
  // I just issued aren't there." Persist the last (kind, subject) in
  // localStorage and auto-restore on mount; the matching useEffect
  // below kicks fetchList once the restore completes.
  //
  // Tenant-scoped key so two tenants on the same browser profile
  // don't bleed each other's last-used principal.
  const lastBrowseKey = `paladin:capabilities:lastBrowse:${tenantId || "_"}`;
  // Lazy-init from localStorage so a return visit pre-fills the last browsed
  // principal — replaces the old hydrate effect. tenantId is available
  // synchronously here (the page renders inside the resolved TenantLayout).
  const [principalKind, setPrincipalKind] = useState<string>(
    () => readLastBrowse(lastBrowseKey)?.kind ?? String(PrincipalKind.AGENT),
  );
  const [subject, setSubject] = useState(
    () => readLastBrowse(lastBrowseKey)?.subject ?? "",
  );
  const [includeExpired, setIncludeExpired] = useState(false);
  const [includeRevoked, setIncludeRevoked] = useState(false);

  const browseQuery = useQuery({
    queryKey: [
      "capabilities",
      tenantId,
      principalKind,
      subject.trim(),
      includeExpired,
      includeRevoked,
    ],
    // Only browse once a principal is chosen — List is principal-scoped.
    enabled: !!tenantId && !!subject.trim(),
    retry: false, // queryFn toasts real failures.
    queryFn: async ({ signal }) => {
      try {
        const res = await capabilityClient.list(
          {
            tenantId,
            principalKind: Number(principalKind),
            subject: subject.trim(),
            includeExpired,
            includeRevoked,
            pageSize: 100,
          },
          { signal },
        );
        // Persist the successful browse so a return visit pre-fills it.
        try {
          window.localStorage.setItem(
            lastBrowseKey,
            JSON.stringify({ kind: principalKind, subject: subject.trim() }),
          );
        } catch {
          // private-mode / quota — non-fatal.
        }
        // Fan-out usage fetches in parallel — each is a single index hit, so
        // 100 in flight is fine. NotFound ⇒ "never" sentinel. The shared
        // signal cancels them all if the browse is superseded.
        const entries = await Promise.all(
          res.capabilities.map(async (c) => {
            try {
              const u = await capabilityClient.getUsage(
                { id: c.id },
                { signal },
              );
              return [
                c.id,
                {
                  requestCount: u.requestCount,
                  spentAmount: fromMicros(u.spentMicros),
                  // UsageRecord without a unit_code is metering-only → UNIT.
                  unitCode: u.unitCode || "UNIT",
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
        return { items: res.capabilities, usage };
      } catch (err) {
        showNotification({
          type: "error",
          title: "Load failed",
          message: errorMessage(err, "Failed to list capabilities"),
        });
        throw err;
      }
    },
  });
  const items = useMemo(
    () => browseQuery.data?.items ?? [],
    [browseQuery.data],
  );
  const usage = browseQuery.data?.usage ?? EMPTY_USAGE;
  const loading = browseQuery.isFetching;
  const hasFetched = browseQuery.isFetched;
  // refetch() runs even while the query is disabled, so guard it the same
  // way: with no principal chosen there is nothing to list, and List refuses
  // an empty subject.
  const fetchList = async () => {
    if (subject.trim()) await browseQuery.refetch();
  };

  // Browse filter + persistence are now driven by the query itself: the
  // (kind, subject, filters) tuple is the queryKey, so changing any of them
  // refetches automatically (and the signal cancels a superseded browse);
  // the queryFn persists the last successful browse to localStorage. Initial
  // restore happens via the lazy useState initializers above — no effects.

  // ── issue dialog ────────────────────────────────────────────────────
  const [createOpen, setCreateOpen] = useState(false);
  // The Issue dialog owns its form + reveal state. The page keeps only the
  // open flag (above) + the two callbacks the dialog needs:

  // onIssued — sync the browse filter to the just-issued (kind, subject) and
  // seed the list so the capability is visible after the reveal panel closes;
  // List is principal-scoped, so without this the page reads empty. Persist
  // the principal so a reload / navigation back restores it.
  const handleIssued = (
    _capability: Capability,
    kind: string,
    subj: string,
  ) => {
    // Switching the browse filter to the just-issued principal changes the
    // query key → the list refetches and shows the new capability (List is
    // principal-scoped). The queryFn persists the new (kind, subject).
    setPrincipalKind(kind);
    setSubject(subj);
  };

  // onClose — close the dialog and refetch so the list reflects the server
  // (the seeded single row is replaced by the authoritative List response).
  const handleIssueClose = () => {
    setCreateOpen(false);
    void fetchList();
  };

  // ── details ─────────────────────────────────────────────────────────
  // Read-only "everything we know about this capability" dialog,
  // opened from the row's kebab menu. Mirrors what the List response
  // already carries plus the usage snapshot we've already fetched —
  // no extra RPC. Token plaintext is NOT shown (the issuer keeps no
  // copy by design; only the freshly-issued reveal panel sees it).
  const [detailsTarget, setDetailsTarget] = useState<Capability | null>(null);

  // ── revoke ──────────────────────────────────────────────────────────
  const [revokeTarget, setRevokeTarget] = useState<Capability | null>(null);
  const [revokeCopyOpen, setRevokeCopyOpen] = useState(false);
  const [copyUsageOpen, setCopyUsageOpen] = useState(false);

  const visibleItems = useMemo(() => items, [items]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">Capabilities</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            Short-lived, signed authorisation tokens for agent / service
            runtimes acting on behalf of{" "}
            <span className="font-mono">{tenant.displayName}</span>.
          </p>
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => void fetchList()}
            disabled={loading || !subject.trim()}
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
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <PlusIcon className="size-4" />
            Issue capability
          </Button>
        </div>
      </div>

      {/* ─── Browse filters ──────────────────────────────────────────── */}
      <Card className="p-4">
        <form
          className="grid grid-cols-1 gap-3 md:grid-cols-[180px_1fr_auto_auto]"
          onSubmit={(e) => {
            e.preventDefault();
            void fetchList();
          }}
        >
          <div className="space-y-1.5">
            <Label className="text-xs">Principal kind</Label>
            <Select
              aria-label="Principal kind"
              options={PRINCIPAL_KIND_OPTIONS}
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
          <div className="flex items-end">
            <Button
              type="submit"
              size="sm"
              disabled={loading || !subject.trim()}
            >
              Browse
            </Button>
          </div>
          <div className="flex items-end gap-3 text-sm">
            <label className="flex cursor-pointer items-center gap-2">
              <input
                type="checkbox"
                checked={includeExpired}
                onChange={(e) => setIncludeExpired(e.target.checked)}
                className="size-4 accent-primary"
              />
              <span className="text-muted-foreground">expired</span>
            </label>
            <label className="flex cursor-pointer items-center gap-2">
              <input
                type="checkbox"
                checked={includeRevoked}
                onChange={(e) => setIncludeRevoked(e.target.checked)}
                className="size-4 accent-primary"
              />
              <span className="text-muted-foreground">revoked</span>
            </label>
          </div>
        </form>
      </Card>

      {/* ─── Result table ────────────────────────────────────────────── */}
      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-70">ID</TableHead>
              <TableHead>Caveats</TableHead>
              <TableHead className="hidden md:table-cell">Audience</TableHead>
              <TableHead className="hidden lg:table-cell">Issued</TableHead>
              <TableHead className="hidden lg:table-cell">Expires</TableHead>
              <TableHead className="hidden xl:table-cell">Usage</TableHead>
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
                  <TableCell colSpan={7} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : !hasFetched ? (
              <TableRow>
                <TableCell colSpan={7} className="h-40 text-center">
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <KeyIcon className="size-8 opacity-40" />
                    <p className="text-sm">
                      Pick a principal and click <em>Browse</em> to list its
                      capabilities.
                    </p>
                  </div>
                </TableCell>
              </TableRow>
            ) : browseQuery.isError && !browseQuery.data ? (
              // "No capabilities for this principal" for a failed list hides live grants.
              <TableRow>
                <TableCell colSpan={7} className="h-40 text-center">
                  <ListLoadError
                    what="Capabilities"
                    reason={errorMessage(browseQuery.error)}
                    onRetry={() => void browseQuery.refetch()}
                  />
                </TableCell>
              </TableRow>
            ) : visibleItems.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="h-40 text-center">
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <KeyIcon className="size-8 opacity-40" />
                    <p className="text-sm">
                      No capabilities for this principal.
                    </p>
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => setCreateOpen(true)}
                    >
                      <PlusIcon className="size-4" />
                      Issue one
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              visibleItems.map((c) => {
                const expired = isExpired(c);
                return (
                  <TableRow key={c.id} className="group">
                    <TableCell>
                      <span
                        className="block max-w-65 truncate font-mono text-xs"
                        title={c.id}
                      >
                        {c.id}
                      </span>
                      {c.parentId && (
                        <span
                          className={cn(
                            T.codeSmall,
                            "block max-w-65 truncate text-muted-foreground",
                          )}
                          title={`parent: ${c.parentId}`}
                        >
                          ↪ {c.parentId.slice(0, 8)}…
                        </span>
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {(c.caveats?.ops ?? []).map((op) => (
                          <Badge
                            key={op}
                            variant="outline"
                            className={cn(T.labelTight, "font-mono")}
                          >
                            {op}
                          </Badge>
                        ))}
                      </div>
                      {(c.caveats?.resourcePrefixes ?? []).length > 0 && (
                        <div
                          className={cn(
                            T.codeSmall,
                            "mt-1 text-muted-foreground",
                          )}
                        >
                          {c.caveats!.resourcePrefixes.join(", ")}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <div className="flex flex-wrap gap-1">
                        {c.audience.map((a) => (
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
                      {formatTimestampUTC(c.issuedAt)}
                    </TableCell>
                    <TableCell
                      className={cn(
                        "hidden lg:table-cell",
                        T.code,
                        "text-muted-foreground",
                      )}
                    >
                      {formatTimestampUTC(c.expiresAt)}
                    </TableCell>
                    <TableCell className={cn("hidden xl:table-cell", T.code)}>
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
                        const reqCap = c.caveats?.maxRequests ?? 0;
                        const budgetCap = fromMicros(
                          c.caveats?.maxBudgetMicros,
                        );
                        // Usage row → caveats → UNIT fallback. The
                        // "USD" default that lived here lied about
                        // the actual unit when both were absent
                        // (legacy / metering-only capabilities).
                        const cellUnit =
                          u.unitCode || c.caveats?.unitCode || "UNIT";
                        return (
                          <div className="space-y-0.5">
                            <div>
                              <span className="text-muted-foreground">
                                req{" "}
                              </span>
                              {u.requestCount.toString()}
                              {reqCap > 0 && (
                                <span className="text-muted-foreground">
                                  {" "}
                                  / {reqCap}
                                </span>
                              )}
                            </div>
                            <div>
                              {formatMoney(
                                u.spentAmount,
                                cellUnit,
                                undefined,
                                4,
                              )}
                              {budgetCap > 0 && (
                                <span className="text-muted-foreground">
                                  {" "}
                                  / {formatMoney(budgetCap, cellUnit)}
                                </span>
                              )}
                            </div>
                          </div>
                        );
                      })()}
                    </TableCell>
                    <TableCell>
                      {expired ? (
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
                      {/* Kebab menu — View details + Revoke. Click on
                          the row's ID won't open details (the row is
                          large and we don't want accidental dialogs);
                          the explicit View action keeps the affordance
                          clear. */}
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
                            onClick={() => setDetailsTarget(c)}
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
      </Card>

      {/* ─── Issue dialog ────────────────────────────────────────────── */}
      <IssueCapabilityDialog
        open={createOpen}
        tenantId={tenantId}
        onClose={handleIssueClose}
        onIssued={handleIssued}
      />

      {/* ─── Details dialog ────────────────────────────────────────── */}
      <CapabilityDetailsDialog
        cap={detailsTarget}
        usageEntry={detailsTarget ? usage.get(detailsTarget.id) : undefined}
        onClose={() => setDetailsTarget(null)}
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
