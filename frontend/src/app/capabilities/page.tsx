"use client";

import { useCallback, useMemo, useState } from "react";
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
import { useTenant } from "@/context/TenantContext";
import { capabilityClient } from "@/lib/connect/client";
import { copyToClipboard, cn } from "@/lib/utils";
import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import { PrincipalKind } from "@/gen/paladin/admin/v1/capability_service_pb";

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
//
// Delegate is intentionally NOT exposed in the operator UI. Delegation
// is an agent-side flow (the agent narrows its own capability when
// spawning a sub-agent); building a Delegate form from the admin
// console invites copy-pasting parent capability ids and producing
// confusing trees. If/when an explicit delegation surface is needed
// we can add a per-row "Delegate" action.

const TTL_OPTIONS: { label: string; value: string; seconds: number | null }[] =
  [
    { label: "5 minutes", value: "5m", seconds: 5 * 60 },
    { label: "1 hour", value: "1h", seconds: 60 * 60 },
    { label: "8 hours", value: "8h", seconds: 8 * 60 * 60 },
    { label: "1 day", value: "1d", seconds: 24 * 60 * 60 },
    { label: "Server default", value: "default", seconds: null },
  ];

// Capability ops vocabulary mirrored from internal/capability/caveats.go.
// Keep the list curated rather than free-form so operators can't typo.
const OP_CHOICES = [
  "get",
  "put",
  "list",
  "delete",
  "presign",
  "tag",
  "search",
  "embed",
  "share",
  "manage",
];

const AUDIENCE_CHOICES = ["data", "admin", "iam", "mcp"];

const PRINCIPAL_KIND_OPTIONS = [
  { value: String(PrincipalKind.USER), label: "user" },
  { value: String(PrincipalKind.AGENT), label: "agent" },
  { value: String(PrincipalKind.SERVICE), label: "service" },
];

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

function isExpired(c: Capability): boolean {
  if (!c.expiresAt) return false;
  const ms = Number(c.expiresAt.seconds) * 1000;
  return ms > 0 && ms < Date.now();
}

export default function CapabilitiesPage() {
  const { tenantId } = useTenant();
  const { showNotification } = useNotification();

  // ── browse filters ──────────────────────────────────────────────────
  const [principalKind, setPrincipalKind] = useState<string>(
    String(PrincipalKind.AGENT),
  );
  const [subject, setSubject] = useState("");
  const [includeExpired, setIncludeExpired] = useState(false);
  const [includeRevoked, setIncludeRevoked] = useState(false);

  const [items, setItems] = useState<Capability[]>([]);
  const [loading, setLoading] = useState(false);
  const [hasFetched, setHasFetched] = useState(false);

  const fetchList = useCallback(async () => {
    if (!tenantId || !subject.trim()) {
      setItems([]);
      setHasFetched(false);
      return;
    }
    setLoading(true);
    try {
      const res = await capabilityClient.list({
        tenantId,
        principalKind: Number(principalKind),
        subject: subject.trim(),
        includeExpired,
        includeRevoked,
        pageSize: 100,
      });
      setItems(res.capabilities);
      setHasFetched(true);
    } catch (err) {
      const msg =
        err instanceof ConnectError
          ? err.rawMessage
          : "Failed to list capabilities";
      showNotification({
        type: "error",
        title: "Load failed",
        message: msg,
      });
      setHasFetched(true);
    } finally {
      setLoading(false);
    }
  }, [
    tenantId,
    principalKind,
    subject,
    includeExpired,
    includeRevoked,
    showNotification,
  ]);

  // ── issue dialog ────────────────────────────────────────────────────
  const [createOpen, setCreateOpen] = useState(false);
  const [issueSubject, setIssueSubject] = useState("");
  const [issuePrincipalKind, setIssuePrincipalKind] = useState<string>(
    String(PrincipalKind.AGENT),
  );
  const [issueOps, setIssueOps] = useState<Set<string>>(
    new Set(["get", "list"]),
  );
  const [issueAudience, setIssueAudience] = useState<Set<string>>(
    new Set(["data"]),
  );
  const [issueResourcePrefixes, setIssueResourcePrefixes] = useState("");
  const [issueMaxRequests, setIssueMaxRequests] = useState("");
  const [issueMaxBudget, setIssueMaxBudget] = useState("");
  const [issueSourceCidr, setIssueSourceCidr] = useState("");
  const [issueTtl, setIssueTtl] = useState("1h");
  const [issuing, setIssuing] = useState(false);

  // After success: { token, capability } reveal panel — one-shot view.
  const [reveal, setReveal] = useState<{
    token: string;
    capabilityId: string;
  } | null>(null);
  const [revealAcknowledged, setRevealAcknowledged] = useState(false);

  const resetIssueForm = () => {
    setIssueSubject("");
    setIssuePrincipalKind(String(PrincipalKind.AGENT));
    setIssueOps(new Set(["get", "list"]));
    setIssueAudience(new Set(["data"]));
    setIssueResourcePrefixes("");
    setIssueMaxRequests("");
    setIssueMaxBudget("");
    setIssueSourceCidr("");
    setIssueTtl("1h");
    setReveal(null);
    setRevealAcknowledged(false);
  };

  const toggleSetEntry = (
    setter: (next: Set<string>) => void,
    current: Set<string>,
    value: string,
  ) => {
    const next = new Set(current);
    if (next.has(value)) next.delete(value);
    else next.add(value);
    setter(next);
  };

  const handleIssue = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!tenantId || !issueSubject.trim()) return;
    if (issueOps.size === 0 || issueAudience.size === 0) {
      showNotification({
        type: "error",
        title: "Validation",
        message: "At least one op and one audience are required.",
      });
      return;
    }
    const ttlSeconds = TTL_OPTIONS.find((o) => o.value === issueTtl)?.seconds;
    const prefixes = issueResourcePrefixes
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    const cidrs = issueSourceCidr
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    const maxRequests = issueMaxRequests
      ? Number.parseInt(issueMaxRequests, 10)
      : 0;
    const maxBudget = issueMaxBudget ? Number.parseFloat(issueMaxBudget) : 0;

    setIssuing(true);
    try {
      const res = await capabilityClient.issue({
        subject: {
          kind: Number(issuePrincipalKind),
          tenantId,
          subject: issueSubject.trim(),
        },
        audience: Array.from(issueAudience),
        caveats: {
          ops: Array.from(issueOps),
          resourcePrefixes: prefixes,
          resourceUris: [],
          maxRequests: Number.isFinite(maxRequests) ? maxRequests : 0,
          maxBudgetUsd: Number.isFinite(maxBudget) ? maxBudget : 0,
          allowTaintedRead: false,
          idempotencyKeyRequired: false,
          sourceIpCidr: cidrs,
        },
        ttlSeconds: BigInt(ttlSeconds ?? 0),
      });
      setReveal({
        token: res.token,
        capabilityId: res.capability?.id ?? "",
      });
      // Optimistically prepend if the current browse filter matches.
      if (
        res.capability &&
        Number(principalKind) === Number(issuePrincipalKind) &&
        subject.trim() === issueSubject.trim()
      ) {
        setItems((prev) => [res.capability!, ...prev]);
      }
      showNotification({
        type: "success",
        title: "Capability issued",
        message: "Copy the token now — it can't be shown again.",
      });
    } catch (err) {
      const msg = err instanceof ConnectError ? err.rawMessage : "Issue failed";
      showNotification({
        type: "error",
        title: "Issue failed",
        message: msg,
      });
    } finally {
      setIssuing(false);
    }
  };

  const handleCloseIssueDialog = (open: boolean) => {
    if (open) {
      setCreateOpen(true);
      return;
    }
    if (reveal && !revealAcknowledged) return;
    setCreateOpen(false);
    resetIssueForm();
    void fetchList();
  };

  const copyToken = useCallback(async () => {
    if (!reveal) return;
    const ok = await copyToClipboard(reveal.token);
    showNotification({
      type: ok ? "success" : "error",
      title: ok ? "Copied" : "Copy failed",
      message: ok
        ? "Capability token copied to clipboard."
        : "Clipboard unavailable.",
    });
  }, [reveal, showNotification]);

  // ── revoke ──────────────────────────────────────────────────────────
  const [revokeTarget, setRevokeTarget] = useState<Capability | null>(null);
  const [revokeCascade, setRevokeCascade] = useState(false);
  const [revokeReason, setRevokeReason] = useState("");

  const handleRevoke = async () => {
    if (!revokeTarget) return;
    try {
      await capabilityClient.revoke({
        id: revokeTarget.id,
        reason: revokeReason.trim(),
        cascadeChildren: revokeCascade,
      });
      showNotification({
        type: "success",
        title: "Revoked",
        message: revokeTarget.id,
      });
      setRevokeTarget(null);
      setRevokeCascade(false);
      setRevokeReason("");
      void fetchList();
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

  const visibleItems = useMemo(() => items, [items]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Capabilities"
        description="Short-lived, signed authorisation tokens for agent and service runtimes."
        showDefaultActions={false}
        actions={
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
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <PlusIcon className="size-4" />
              Issue capability
            </Button>
          </div>
        }
      />

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
              <TableHead className="w-[280px]">ID</TableHead>
              <TableHead>Caveats</TableHead>
              <TableHead className="hidden md:table-cell">Audience</TableHead>
              <TableHead className="hidden lg:table-cell">Issued</TableHead>
              <TableHead className="hidden lg:table-cell">Expires</TableHead>
              <TableHead className="w-[100px]">Status</TableHead>
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
                        className="block max-w-[260px] truncate font-mono text-xs"
                        title={c.id}
                      >
                        {c.id}
                      </span>
                      {c.parentId && (
                        <span
                          className="block max-w-[260px] truncate font-mono text-[10px] text-muted-foreground"
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
                            className="font-mono text-[10px]"
                          >
                            {op}
                          </Badge>
                        ))}
                      </div>
                      {(c.caveats?.resourcePrefixes ?? []).length > 0 && (
                        <div className="mt-1 font-mono text-[10px] text-muted-foreground">
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
                            className="text-[10px]"
                          >
                            {a}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="hidden lg:table-cell font-mono text-[11px] text-muted-foreground">
                      {formatTimestamp(c.issuedAt)}
                    </TableCell>
                    <TableCell className="hidden lg:table-cell font-mono text-[11px] text-muted-foreground">
                      {formatTimestamp(c.expiresAt)}
                    </TableCell>
                    <TableCell>
                      {expired ? (
                        <Badge variant="outline" className="text-[11px]">
                          expired
                        </Badge>
                      ) : (
                        <Badge variant="success" className="text-[11px]">
                          active
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-8 opacity-60 group-hover:opacity-100"
                        aria-label={`Revoke ${c.id}`}
                        onClick={() => setRevokeTarget(c)}
                      >
                        <TrashIcon className="size-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>

      {/* ─── Issue dialog ────────────────────────────────────────────── */}
      <Dialog open={createOpen} onOpenChange={handleCloseIssueDialog}>
        <DialogContent className="max-w-2xl">
          {reveal ? (
            <div>
              <DialogHeader>
                <DialogTitle className="flex items-center gap-2">
                  <CheckCircleIcon className="size-5 text-emerald-500" />
                  Capability issued
                </DialogTitle>
                <DialogDescription>
                  Copy the JWT now — the control plane only stores its metadata,
                  the compact token is shown exactly once.
                </DialogDescription>
              </DialogHeader>
              <div className="space-y-3 py-4">
                <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">
                  <ExclamationTriangleIcon className="mr-1 inline-block size-4 align-text-bottom" />
                  Hand this to the agent over a secure channel — anyone with the
                  JWT can act under the caveats until expiry.
                </div>
                <div className="space-y-1.5">
                  <Label className="text-xs">Capability JWT</Label>
                  <div className="flex gap-2">
                    <textarea
                      readOnly
                      value={reveal.token}
                      className="flex-1 rounded-md border border-input bg-background px-2 py-2 font-mono text-xs"
                      rows={4}
                      onFocus={(e) => e.currentTarget.select()}
                    />
                    <Button variant="outline" onClick={copyToken} type="button">
                      <ClipboardDocumentIcon className="size-4" />
                      Copy
                    </Button>
                  </div>
                  <p className="font-mono text-[11px] text-muted-foreground">
                    id: {reveal.capabilityId}
                  </p>
                </div>
                <label className="flex cursor-pointer items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={revealAcknowledged}
                    onChange={(e) => setRevealAcknowledged(e.target.checked)}
                    className="size-4 accent-primary"
                  />
                  <span>I&apos;ve handed this token to the agent.</span>
                </label>
              </div>
              <DialogFooter>
                <Button
                  type="button"
                  onClick={() => handleCloseIssueDialog(false)}
                  disabled={!revealAcknowledged}
                >
                  Done
                </Button>
              </DialogFooter>
            </div>
          ) : (
            <form onSubmit={handleIssue}>
              <DialogHeader>
                <DialogTitle>Issue capability</DialogTitle>
                <DialogDescription>
                  Mint a signed JWT for an agent or service. Caveats are
                  enforced server-side on every RPC.
                </DialogDescription>
              </DialogHeader>
              <div className="space-y-4 py-4">
                {/* Principal */}
                <div className="grid grid-cols-1 gap-3 md:grid-cols-[180px_1fr]">
                  <div className="space-y-1.5">
                    <Label className="text-xs">Principal kind</Label>
                    <Select
                      options={PRINCIPAL_KIND_OPTIONS}
                      value={issuePrincipalKind}
                      onChange={setIssuePrincipalKind}
                      className="w-full"
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label className="text-xs" htmlFor="cap-issue-subj">
                      Subject
                    </Label>
                    <Input
                      id="cap-issue-subj"
                      autoFocus
                      placeholder="agent-id / user subject / service-account name"
                      value={issueSubject}
                      onChange={(e) => setIssueSubject(e.target.value)}
                    />
                  </div>
                </div>

                {/* Ops */}
                <div className="space-y-1.5">
                  <Label className="text-xs">Allowed ops</Label>
                  <div className="flex flex-wrap gap-2">
                    {OP_CHOICES.map((op) => (
                      <label
                        key={op}
                        className={cn(
                          "flex cursor-pointer items-center gap-1.5 rounded-md border px-2 py-1 text-xs",
                          issueOps.has(op)
                            ? "border-primary bg-primary/10"
                            : "border-input",
                        )}
                      >
                        <input
                          type="checkbox"
                          checked={issueOps.has(op)}
                          onChange={() =>
                            toggleSetEntry(setIssueOps, issueOps, op)
                          }
                          className="size-3 accent-primary"
                        />
                        <span className="font-mono">{op}</span>
                      </label>
                    ))}
                  </div>
                </div>

                {/* Audience */}
                <div className="space-y-1.5">
                  <Label className="text-xs">Audience (planes)</Label>
                  <div className="flex flex-wrap gap-2">
                    {AUDIENCE_CHOICES.map((aud) => (
                      <label
                        key={aud}
                        className={cn(
                          "flex cursor-pointer items-center gap-1.5 rounded-md border px-2 py-1 text-xs",
                          issueAudience.has(aud)
                            ? "border-primary bg-primary/10"
                            : "border-input",
                        )}
                      >
                        <input
                          type="checkbox"
                          checked={issueAudience.has(aud)}
                          onChange={() =>
                            toggleSetEntry(setIssueAudience, issueAudience, aud)
                          }
                          className="size-3 accent-primary"
                        />
                        <span className="font-mono">{aud}</span>
                      </label>
                    ))}
                  </div>
                </div>

                {/* Resource prefixes */}
                <div className="space-y-1.5">
                  <Label className="text-xs" htmlFor="cap-prefix">
                    Resource prefixes (optional)
                  </Label>
                  <Input
                    id="cap-prefix"
                    placeholder="objects/contracts/2026/, buckets/acme-prod"
                    value={issueResourcePrefixes}
                    onChange={(e) => setIssueResourcePrefixes(e.target.value)}
                  />
                  <p className="text-[11px] text-muted-foreground">
                    Comma- or space-separated. Empty means &ldquo;no prefix
                    restriction&rdquo;.
                  </p>
                </div>

                {/* Quotas + CIDR */}
                <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
                  <div className="space-y-1.5">
                    <Label className="text-xs" htmlFor="cap-maxreq">
                      Max requests
                    </Label>
                    <Input
                      id="cap-maxreq"
                      type="number"
                      min={0}
                      placeholder="0 = unlimited"
                      value={issueMaxRequests}
                      onChange={(e) => setIssueMaxRequests(e.target.value)}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label className="text-xs" htmlFor="cap-maxbudget">
                      Max budget USD
                    </Label>
                    <Input
                      id="cap-maxbudget"
                      type="number"
                      step="0.01"
                      min={0}
                      placeholder="0 = unlimited"
                      value={issueMaxBudget}
                      onChange={(e) => setIssueMaxBudget(e.target.value)}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label className="text-xs">TTL</Label>
                    <Select
                      options={TTL_OPTIONS.map((o) => ({
                        value: o.value,
                        label: o.label,
                      }))}
                      value={issueTtl}
                      onChange={setIssueTtl}
                      className="w-full"
                    />
                  </div>
                </div>

                {/* CIDR */}
                <div className="space-y-1.5">
                  <Label className="text-xs" htmlFor="cap-cidr">
                    Source IP CIDR (optional)
                  </Label>
                  <Input
                    id="cap-cidr"
                    placeholder="10.0.0.0/8, 192.168.1.0/24"
                    value={issueSourceCidr}
                    onChange={(e) => setIssueSourceCidr(e.target.value)}
                  />
                </div>
              </div>
              <DialogFooter>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => handleCloseIssueDialog(false)}
                >
                  Cancel
                </Button>
                <Button
                  type="submit"
                  disabled={
                    issuing ||
                    !issueSubject.trim() ||
                    issueOps.size === 0 ||
                    issueAudience.size === 0 ||
                    !tenantId
                  }
                >
                  {issuing ? "Issuing…" : "Issue capability"}
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>

      {/* ─── Revoke confirm ────────────────────────────────────────── */}
      <AlertDialog
        open={!!revokeTarget}
        onOpenChange={(o) => {
          if (!o) {
            setRevokeTarget(null);
            setRevokeCascade(false);
            setRevokeReason("");
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke this capability?</AlertDialogTitle>
            <AlertDialogDescription>
              <span className="font-mono text-foreground">
                {revokeTarget?.id}
              </span>{" "}
              will stop verifying immediately on every plane. Revocation is
              checked locally by interceptors using a denylist that purges after
              the natural expiry, so the cost of revoking is bounded.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div className="space-y-3 py-2">
            <div className="space-y-1.5">
              <Label className="text-xs" htmlFor="revoke-reason">
                Reason (optional)
              </Label>
              <Input
                id="revoke-reason"
                placeholder="leaked / superseded / agent retired"
                value={revokeReason}
                onChange={(e) => setRevokeReason(e.target.value)}
              />
            </div>
            <label className="flex cursor-pointer items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={revokeCascade}
                onChange={(e) => setRevokeCascade(e.target.checked)}
                className="size-4 accent-primary"
              />
              <span>
                Cascade to children — revoke every delegation under this one.
              </span>
            </label>
          </div>
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
