"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ClipboardDocumentIcon,
  EllipsisVerticalIcon,
  ExclamationTriangleIcon,
  EyeIcon,
  KeyIcon,
  PlusIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { ConnectError, Code } from "@connectrpc/connect";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { ChipInput } from "@/components/ui/ChipInput";
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
import { capabilityClient } from "@/lib/connect/client";
import { copyToClipboard, cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
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

// ─── Issue-dialog layout helpers ───────────────────────────────────────────

// FormSection groups related fields under a small heading so the
// dialog reads as four distinct beats (Principal → Authorization →
// Restrictions → Limits) rather than a flat 7-field stack.
function FormSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-3">
      <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {title}
      </h3>
      <div className="space-y-3">{children}</div>
    </div>
  );
}

// Field is one labelled control. `optional` flag drops a low-contrast
// "(optional)" tag next to the label so we don't have to bake the
// hint into the label string itself; `hint` renders below the control
// in muted small text.
function Field({
  label,
  htmlFor,
  optional,
  hint,
  children,
}: {
  label: string;
  htmlFor?: string;
  optional?: boolean;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor} className="text-xs">
        {label}
        {optional && (
          <span className="ml-1.5 font-normal text-muted-foreground">
            (optional)
          </span>
        )}
      </Label>
      {children}
      {hint && <p className={T.hint}>{hint}</p>}
    </div>
  );
}

// ToggleRow renders a horizontal row of pill-buttons for multi-select
// enums. Replaces the previous bare-checkbox-with-label pattern: the
// active state is now obvious (filled background) without relying on
// a tiny native checkbox to read the truth, and keyboard activation
// works through standard button semantics (Enter / Space) without us
// re-implementing it.
function ToggleRow({
  options,
  selected,
  onToggle,
}: {
  options: readonly string[];
  selected: Set<string>;
  onToggle: (value: string) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((opt) => {
        const active = selected.has(opt);
        return (
          <button
            key={opt}
            type="button"
            onClick={() => onToggle(opt)}
            aria-pressed={active}
            className={cn(
              "rounded-md border px-2.5 py-1 font-mono text-xs transition-colors",
              active
                ? "border-primary bg-primary/10 text-primary"
                : "border-input bg-background hover:bg-muted",
            )}
          >
            {opt}
          </button>
        );
      })}
    </div>
  );
}

// LimitInput pairs a numeric input with an "Unlimited" toggle. The
// previous "0 = unlimited" placeholder convention misread as
// "0 = forbidden / nothing allowed" — exactly the opposite of the
// protocol's intent. Now the toggle is the explicit unlimited
// signal: when on, the input is disabled (grey, empty, ignored at
// submit), the wire value is 0; when off, the input must hold a
// positive number or submit is blocked. Internal protocol unchanged.
function LimitInput({
  id,
  value,
  onChange,
  unlimited,
  onUnlimitedChange,
  placeholder,
  type = "number",
  step,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  unlimited: boolean;
  onUnlimitedChange: (next: boolean) => void;
  placeholder?: string;
  type?: string;
  step?: string;
}) {
  return (
    // flex-1 + min-w-0 on the Input is the fix for the previous
    // collapsed-input bug — type=number renders as a spinner-only
    // ~30px stub by default in flex containers because the input's
    // natural width is content-based and there's no flex hint. The
    // pair pushes it to fill the row, with the Unlimited button
    // sitting at its content width on the right.
    <div className="flex items-center gap-1.5">
      <Input
        id={id}
        type={type}
        step={step}
        min={0}
        value={unlimited ? "" : value}
        onChange={(e) => onChange(e.target.value)}
        disabled={unlimited}
        placeholder={unlimited ? "Unlimited" : placeholder}
        className={cn("min-w-0 flex-1", unlimited && "italic")}
      />
      <button
        type="button"
        onClick={() => onUnlimitedChange(!unlimited)}
        aria-pressed={unlimited}
        className={cn(
          "h-9 shrink-0 rounded-md border px-3 text-xs font-medium transition-colors",
          unlimited
            ? "border-primary bg-primary/10 text-primary"
            : "border-input bg-background hover:bg-muted",
        )}
      >
        Unlimited
      </button>
    </div>
  );
}

// DetailsBody renders every field of a Capability in a stacked
// label/value layout. Long mono strings (id, parent_id, prefixes)
// `break-all` so the dialog doesn't blow out horizontally on UUIDs.
function DetailsBody({
  cap,
  usageEntry,
}: {
  cap: Capability;
  usageEntry: { requestCount: bigint; spentUsd: number } | "never" | undefined;
}) {
  const principalKindLabel =
    PRINCIPAL_KIND_OPTIONS.find((o) => Number(o.value) === cap.subject?.kind)
      ?.label ?? `kind:${cap.subject?.kind}`;
  const expired = isExpired(cap);

  return (
    // CSS multi-column layout (columns-2 on md+) with
    // `break-inside-avoid` on each section. Browser balances the
    // column heights automatically and never splits a section
    // mid-row, so the dialog ends ~half as tall as the previous
    // single-column stack. Document order is the read order an
    // operator wants: Identity → Principal → Authorization (who/
    // what), then Caveats → Limits & usage → Lifetime (policy/
    // accounting).
    //
    // Sections are wrapped in a div with `mb-4 break-inside-avoid`
    // (applied on DetailsSection root) so they flow as units.
    <div className="md:columns-2 md:gap-x-8 py-1">
      <DetailsSection title="Identity">
        <DetailRow label="Capability ID" value={cap.id} mono breakAll />
        <DetailRow label="Issuer" value={cap.issuer || "—"} mono />
        <DetailRow
          label="Parent ID"
          value={cap.parentId || "—"}
          mono
          breakAll
        />
        <DetailRow label="Generation" value={cap.generation.toString()} mono />
      </DetailsSection>

      <DetailsSection title="Principal">
        <DetailRow label="Kind" value={principalKindLabel} />
        <DetailRow
          label="Subject"
          value={cap.subject?.subject || "—"}
          mono
          breakAll
        />
        <DetailRow
          label="Tenant ID"
          value={cap.subject?.tenantId || "—"}
          mono
          breakAll
        />
      </DetailsSection>

      <DetailsSection title="Authorization">
        <DetailRow
          label="Audience"
          value={
            cap.audience.length > 0 ? (
              <div className="flex flex-wrap gap-1">
                {cap.audience.map((a) => (
                  <Badge key={a} variant="secondary" className={T.code}>
                    {a}
                  </Badge>
                ))}
              </div>
            ) : (
              "—"
            )
          }
        />
        <DetailRow
          label="Allowed ops"
          value={
            (cap.caveats?.ops ?? []).length > 0 ? (
              <div className="flex flex-wrap gap-1">
                {cap.caveats!.ops.map((op) => (
                  <Badge key={op} variant="outline" className={T.code}>
                    {op}
                  </Badge>
                ))}
              </div>
            ) : (
              "—"
            )
          }
        />
      </DetailsSection>

      <DetailsSection title="Caveats">
        <DetailRow
          label="Resource prefixes"
          value={
            (cap.caveats?.resourcePrefixes ?? []).length > 0 ? (
              <ul className="space-y-0.5 font-mono text-xs">
                {cap.caveats!.resourcePrefixes.map((p) => (
                  <li key={p} className="break-all">
                    {p}
                  </li>
                ))}
              </ul>
            ) : (
              "no restriction"
            )
          }
        />
        <DetailRow
          label="Resource URIs"
          value={
            (cap.caveats?.resourceUris ?? []).length > 0 ? (
              <ul className="space-y-0.5 font-mono text-xs">
                {cap.caveats!.resourceUris.map((u) => (
                  <li key={u} className="break-all">
                    {u}
                  </li>
                ))}
              </ul>
            ) : (
              "no restriction"
            )
          }
        />
        <DetailRow
          label="Source IP CIDR"
          value={
            (cap.caveats?.sourceIpCidr ?? []).length > 0 ? (
              <div className="flex flex-wrap gap-1">
                {cap.caveats!.sourceIpCidr.map((c) => (
                  <Badge key={c} variant="outline" className={T.code}>
                    {c}
                  </Badge>
                ))}
              </div>
            ) : (
              "any IP"
            )
          }
        />
        <DetailRow
          label="Allow tainted read"
          value={cap.caveats?.allowTaintedRead ? "yes" : "no"}
        />
        <DetailRow
          label="Idempotency key required"
          value={cap.caveats?.idempotencyKeyRequired ? "yes" : "no"}
        />
      </DetailsSection>

      <DetailsSection title="Limits & usage">
        <DetailRow
          label="Max requests"
          value={
            (cap.caveats?.maxRequests ?? 0) > 0
              ? cap.caveats!.maxRequests.toString()
              : "unlimited"
          }
          mono
        />
        <DetailRow
          label="Requests used"
          value={
            usageEntry === undefined
              ? "loading…"
              : usageEntry === "never"
                ? "never used"
                : usageEntry.requestCount.toString()
          }
          mono
        />
        <DetailRow
          label="Max budget USD"
          value={
            (cap.caveats?.maxBudgetUsd ?? 0) > 0
              ? `$${cap.caveats!.maxBudgetUsd.toFixed(2)}`
              : "unlimited"
          }
          mono
        />
        <DetailRow
          label="Spent USD"
          value={
            usageEntry === undefined
              ? "loading…"
              : usageEntry === "never"
                ? "$0.0000"
                : `$${usageEntry.spentUsd.toFixed(4)}`
          }
          mono
        />
      </DetailsSection>

      <DetailsSection title="Lifetime">
        <DetailRow
          label="Status"
          value={
            <Badge variant={expired ? "outline" : "success"} className={T.code}>
              {expired ? "expired" : "active"}
            </Badge>
          }
        />
        <DetailRow label="Issued" value={formatTimestamp(cap.issuedAt)} mono />
        <DetailRow
          label="Not before"
          value={formatTimestamp(cap.notBefore)}
          mono
        />
        <DetailRow
          label="Expires"
          value={formatTimestamp(cap.expiresAt)}
          mono
        />
      </DetailsSection>
    </div>
  );
}

function DetailsSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    // `break-inside-avoid` keeps the section as one block when the
    // parent uses CSS multi-column (DetailsBody on md+). Without it
    // the browser is free to split a section mid-dl, leaving the
    // heading at the bottom of column 1 and the rows orphaned at
    // the top of column 2. `mb-4` provides the inter-section
    // breathing room that the previous `space-y-4` parent gave us.
    <div className="mb-4 break-inside-avoid space-y-2 last:mb-0">
      <h3 className={T.label}>{title}</h3>
      <dl className="grid grid-cols-[140px_1fr] gap-x-3 gap-y-1.5 text-sm">
        {children}
      </dl>
    </div>
  );
}

function DetailRow({
  label,
  value,
  mono,
  breakAll,
}: {
  label: string;
  value: React.ReactNode;
  mono?: boolean;
  breakAll?: boolean;
}) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd
        className={cn(
          mono && "font-mono text-xs",
          breakAll && "break-all",
          "min-w-0",
        )}
      >
        {value}
      </dd>
    </>
  );
}

function isExpired(c: Capability): boolean {
  if (!c.expiresAt) return false;
  const ms = Number(c.expiresAt.seconds) * 1000;
  return ms > 0 && ms < Date.now();
}

export default function CapabilitiesPage() {
  const { tenantId } = useScope();
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
  const [principalKind, setPrincipalKind] = useState<string>(
    String(PrincipalKind.AGENT),
  );
  const [subject, setSubject] = useState("");
  const [includeExpired, setIncludeExpired] = useState(false);
  const [includeRevoked, setIncludeRevoked] = useState(false);

  // hydrated guards the auto-restore + auto-fetch effects so we don't
  // fire fetchList against an empty filter on the very first render
  // before localStorage has been read. Set true once the restore
  // attempt completes, regardless of whether anything was actually
  // restored.
  const hydratedRef = useRef(false);

  const [items, setItems] = useState<Capability[]>([]);
  const [loading, setLoading] = useState(false);
  const [hasFetched, setHasFetched] = useState(false);

  // Per-capability usage snapshots fetched after list. Map keyed by
  // cap.id; absent ⇒ never used (NOT_FOUND), pending ⇒ fetch
  // in-flight. Re-fetched whenever the list refreshes.
  const [usage, setUsage] = useState<
    Map<string, { requestCount: bigint; spentUsd: number } | "never">
  >(new Map());

  const fetchList = useCallback(async () => {
    if (!tenantId || !subject.trim()) {
      setItems([]);
      setUsage(new Map());
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

      // Fan-out usage fetches in parallel. Each cap's request is
      // a single Postgres index hit on the server, so 100 rows in
      // flight is fine. NOT_FOUND maps to "never" sentinel.
      void Promise.all(
        res.capabilities.map(async (c) => {
          try {
            const u = await capabilityClient.getUsage({ id: c.id });
            return [
              c.id,
              { requestCount: u.requestCount, spentUsd: u.spentUsd },
            ] as const;
          } catch (err) {
            if (err instanceof ConnectError && err.code === Code.NotFound) {
              return [c.id, "never" as const] as const;
            }
            return null;
          }
        }),
      ).then((entries) => {
        setUsage(
          new Map(
            entries.filter(
              (
                e,
              ): e is readonly [
                string,
                { requestCount: bigint; spentUsd: number } | "never",
              ] => e !== null,
            ),
          ),
        );
      });
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

  // ── restore last browsed principal on mount ────────────────────────
  // Read from localStorage once tenantId is available (the storage key
  // is tenant-scoped so we can't read it during render-0 before
  // useTenant resolves). Set state synchronously and mark hydrated; a
  // separate effect picks up the state change and calls fetchList.
  useEffect(() => {
    if (!tenantId || hydratedRef.current) return;
    try {
      const raw = window.localStorage.getItem(lastBrowseKey);
      if (raw) {
        const saved = JSON.parse(raw) as {
          kind?: string;
          subject?: string;
        };
        if (saved.kind) setPrincipalKind(saved.kind);
        if (saved.subject) setSubject(saved.subject);
      }
    } catch {
      // localStorage can throw in private-mode / quota scenarios — fall
      // through to the default (empty subject) state.
    }
    hydratedRef.current = true;
  }, [tenantId, lastBrowseKey]);

  // Auto-fetch whenever the browse filter changes after hydration.
  // Only fires once hydratedRef is set so we don't issue an empty-
  // subject request on render-0. After the restore effect runs and
  // sets subject/principalKind, this effect reacts to the state
  // update and calls fetchList with the restored values.
  //
  // Side effect: persist the new (kind, subject) so a manual Browse
  // click is remembered across reloads, not just the post-Issue
  // synchronisation. The persist only fires when subject is non-
  // empty — empty would clobber a previously-saved value with a
  // useless filter.
  useEffect(() => {
    if (!hydratedRef.current) return;
    if (!tenantId || !subject.trim()) return;
    void fetchList();
    try {
      window.localStorage.setItem(
        lastBrowseKey,
        JSON.stringify({ kind: principalKind, subject: subject.trim() }),
      );
    } catch {
      // private-mode / quota — non-fatal.
    }
  }, [
    tenantId,
    principalKind,
    subject,
    includeExpired,
    includeRevoked,
    fetchList,
    lastBrowseKey,
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
  // Chip-typed lists rather than free-text strings — the ChipInput
  // commits one trimmed, deduped token per Enter / comma / space, so
  // the state shape matches the API shape and there's no submit-time
  // parse step to get wrong.
  const [issueResourcePrefixes, setIssueResourcePrefixes] = useState<string[]>(
    [],
  );
  const [issueSourceCidr, setIssueSourceCidr] = useState<string[]>([]);
  // Quotas have an explicit "Unlimited" toggle rather than the
  // "0 = unlimited" placeholder convention. The protocol still uses 0
  // to mean "no cap" (see internal/capability/types.go.Caveats), but
  // surfacing 0 in a number input reads as "forbidden / nothing
  // allowed" — exactly the opposite. The toggle disables the input
  // and submits 0 on the wire; toggling off requires a positive
  // number. Default ON so the previous "leave it blank to skip"
  // ergonomics still work.
  const [issueMaxRequests, setIssueMaxRequests] = useState("");
  const [issueMaxRequestsUnlimited, setIssueMaxRequestsUnlimited] =
    useState(true);
  const [issueMaxBudget, setIssueMaxBudget] = useState("");
  const [issueMaxBudgetUnlimited, setIssueMaxBudgetUnlimited] = useState(true);
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
    setIssueResourcePrefixes([]);
    setIssueSourceCidr([]);
    setIssueMaxRequests("");
    setIssueMaxRequestsUnlimited(true);
    setIssueMaxBudget("");
    setIssueMaxBudgetUnlimited(true);
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
    // ChipInput already trimmed/deduped, but mirror the same flatten
    // here so a user who types into the input and submits without
    // pressing Enter still has their pending draft included. The
    // ChipInput commits on blur (which form submission triggers via
    // focus loss), so this is belt-and-braces — duplicates filtered.
    const prefixes = issueResourcePrefixes;
    const cidrs = issueSourceCidr;
    // Unlimited toggle wins: protocol's 0 = "no cap". When the toggle
    // is off the user must have entered a positive number — block
    // submit otherwise so the dialog never silently sends 0 = forbid.
    const maxRequests = issueMaxRequestsUnlimited
      ? 0
      : Number.parseInt(issueMaxRequests, 10);
    const maxBudget = issueMaxBudgetUnlimited
      ? 0
      : Number.parseFloat(issueMaxBudget);
    if (
      !issueMaxRequestsUnlimited &&
      (!Number.isFinite(maxRequests) || maxRequests <= 0)
    ) {
      showNotification({
        type: "error",
        title: "Validation",
        message: "Max requests must be a positive number, or toggle Unlimited.",
      });
      return;
    }
    if (
      !issueMaxBudgetUnlimited &&
      (!Number.isFinite(maxBudget) || maxBudget <= 0)
    ) {
      showNotification({
        type: "error",
        title: "Validation",
        message: "Max budget must be a positive number, or toggle Unlimited.",
      });
      return;
    }

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
          // Validation above guarantees these are sane: either
          // explicitly toggled unlimited (→ 0) or a finite > 0.
          maxRequests,
          maxBudgetUsd: maxBudget,
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
      // Sync the browse filter to the just-issued (kind, subject) so
      // the list the user lands on after closing the reveal panel
      // shows the capability they just minted. Without this, the
      // page typically shows an empty list because List is
      // principal-scoped — the user has to manually retype the
      // subject they just typed in the Issue form to see anything.
      // We also seed `items` with the freshly returned capability so
      // the list is non-empty during the moment between dialog close
      // and the post-close fetchList completing.
      if (res.capability) {
        setPrincipalKind(issuePrincipalKind);
        setSubject(issueSubject.trim());
        setItems([res.capability]);
        setHasFetched(true);
        // Persist so the next page load / navigation back to
        // /capabilities restores this principal. Without this, the
        // list is principal-scoped and any reload makes the just-
        // issued capability "disappear" from the user's perspective.
        try {
          window.localStorage.setItem(
            lastBrowseKey,
            JSON.stringify({
              kind: issuePrincipalKind,
              subject: issueSubject.trim(),
            }),
          );
        } catch {
          // private-mode / quota — silent fall-through; the page
          // still works for the current session.
        }
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

  // ── details ─────────────────────────────────────────────────────────
  // Read-only "everything we know about this capability" dialog,
  // opened from the row's kebab menu. Mirrors what the List response
  // already carries plus the usage snapshot we've already fetched —
  // no extra RPC. Token plaintext is NOT shown (the issuer keeps no
  // copy by design; only the freshly-issued reveal panel sees it).
  const [detailsTarget, setDetailsTarget] = useState<Capability | null>(null);

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
              <TableHead className="hidden xl:table-cell">Usage</TableHead>
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
                          className={cn(
                            T.codeSmall,
                            "block max-w-[260px] truncate text-muted-foreground",
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
                      {formatTimestamp(c.issuedAt)}
                    </TableCell>
                    <TableCell
                      className={cn(
                        "hidden lg:table-cell",
                        T.code,
                        "text-muted-foreground",
                      )}
                    >
                      {formatTimestamp(c.expiresAt)}
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
                        const budgetCap = c.caveats?.maxBudgetUsd ?? 0;
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
                              <span className="text-muted-foreground">$ </span>
                              {u.spentUsd.toFixed(4)}
                              {budgetCap > 0 && (
                                <span className="text-muted-foreground">
                                  {" "}
                                  / {budgetCap.toFixed(2)}
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
      <Dialog open={createOpen} onOpenChange={handleCloseIssueDialog}>
        {/* Width override pitfall: shadcn DialogContent's base classes
            include `sm:max-w-sm` (384px), which tailwind-merge keeps
            at the sm breakpoint alongside our `max-w-4xl` because the
            two are at different responsive specificities. Spelling
            the override with a responsive prefix (`sm:max-w-5xl`)
            makes the wide value win on every viewport ≥ sm. Result:
            ~1024px instead of 384px on tablet+, room for Allowed ops
            in a single row and the LimitInput trio without crushing. */}
        <DialogContent className="max-w-[calc(100%-2rem)] sm:max-w-5xl">
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
                  <p className={cn(T.code, "text-muted-foreground")}>
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
              <div className="space-y-4 py-2">
                {/* ─── Principal ─────────────────────────────────────
                    Who the capability is issued to. Subject is the
                    stable identity string the verifier sees in the
                    `sub` JWT claim. */}
                <FormSection title="Principal">
                  {/* Kind values are short ("user" / "agent" /
                      "service") — a 180px column was 90% empty.
                      Tighten to 140px so Subject (which holds long
                      free-form names) gets the headroom it needs. */}
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-[140px_1fr]">
                    <Field label="Kind">
                      <Select
                        options={PRINCIPAL_KIND_OPTIONS}
                        value={issuePrincipalKind}
                        onChange={setIssuePrincipalKind}
                        className="w-full"
                      />
                    </Field>
                    <Field label="Subject" htmlFor="cap-issue-subj">
                      <Input
                        id="cap-issue-subj"
                        autoFocus
                        placeholder="agent-id / user subject / service-account name"
                        value={issueSubject}
                        onChange={(e) => setIssueSubject(e.target.value)}
                      />
                    </Field>
                  </div>
                </FormSection>

                {/* ───Authorization ─────────────────────────────────
                    What the capability is allowed to do, on which
                    planes. Both lists are required (verifier rejects
                    capabilities with empty `ops` or `aud`). */}
                <FormSection title="Authorization">
                  {/* Two-column at md+ — Ops takes the wider column
                      because the list is longer; Audience fits in the
                      narrower one. Stacking pushed the dialog past the
                      viewport on shorter screens. */}
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-[3fr_2fr]">
                    <Field label="Allowed ops" hint="At least one required.">
                      <ToggleRow
                        options={OP_CHOICES}
                        selected={issueOps}
                        onToggle={(v) =>
                          toggleSetEntry(setIssueOps, issueOps, v)
                        }
                      />
                    </Field>
                    <Field
                      label="Audience (planes)"
                      hint="Which PALADIN planes accept this token."
                    >
                      <ToggleRow
                        options={AUDIENCE_CHOICES}
                        selected={issueAudience}
                        onToggle={(v) =>
                          toggleSetEntry(setIssueAudience, issueAudience, v)
                        }
                      />
                    </Field>
                  </div>
                </FormSection>

                {/* ───Restrictions ──────────────────────────────────
                    Optional caveats narrowing where the capability
                    can be used. Empty = unrestricted on that axis.
                    Both fields use ChipInput so the parsed token list
                    is always visible — no surprise comma parsing. */}
                <FormSection title="Restrictions">
                  {/* Asymmetric grid: Resource prefixes hold long
                      paths (object keys, bucket prefixes, often
                      40+ chars) and need ~2/3 of the row. Source IP
                      CIDR strings are short (e.g. 10.0.0.0/8 — 10
                      chars) and fit comfortably in the remaining 1/3.
                      A 50/50 split squeezed prefixes too tight. */}
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-[2fr_1fr]">
                    <Field
                      label="Resource prefixes"
                      optional
                      htmlFor="cap-prefix"
                      hint="Enter / comma / space to add. Empty = no restriction."
                    >
                      <ChipInput
                        id="cap-prefix"
                        values={issueResourcePrefixes}
                        onChange={setIssueResourcePrefixes}
                        placeholder="objects/contracts/2026/"
                      />
                    </Field>
                    <Field
                      label="Source IP CIDR"
                      optional
                      htmlFor="cap-cidr"
                      hint="Restrict to clients whose IP is in one of these ranges."
                    >
                      <ChipInput
                        id="cap-cidr"
                        values={issueSourceCidr}
                        onChange={setIssueSourceCidr}
                        placeholder="10.0.0.0/8"
                      />
                    </Field>
                  </div>
                </FormSection>

                {/* ───Limits ────────────────────────────────────────
                    Per-capability quotas + lifetime. 0 = unlimited
                    where applicable. TTL is selected from a curated
                    list to discourage long-lived agent tokens. */}
                <FormSection title="Limits">
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
                    <Field label="Max requests" htmlFor="cap-maxreq">
                      <LimitInput
                        id="cap-maxreq"
                        value={issueMaxRequests}
                        onChange={setIssueMaxRequests}
                        unlimited={issueMaxRequestsUnlimited}
                        onUnlimitedChange={setIssueMaxRequestsUnlimited}
                        placeholder="e.g. 1000"
                      />
                    </Field>
                    <Field label="Max budget USD" htmlFor="cap-maxbudget">
                      <LimitInput
                        id="cap-maxbudget"
                        type="number"
                        step="0.01"
                        value={issueMaxBudget}
                        onChange={setIssueMaxBudget}
                        unlimited={issueMaxBudgetUnlimited}
                        onUnlimitedChange={setIssueMaxBudgetUnlimited}
                        placeholder="e.g. 25.00"
                      />
                    </Field>
                    <Field label="TTL">
                      <Select
                        options={TTL_OPTIONS.map((o) => ({
                          value: o.value,
                          label: o.label,
                        }))}
                        value={issueTtl}
                        onChange={setIssueTtl}
                        className="w-full"
                      />
                    </Field>
                  </div>
                </FormSection>
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

      {/* ─── Details dialog ────────────────────────────────────────── */}
      {/* Read-only display of every field the capability carries.
          Opens from the row kebab menu. No RPC — purely renders the
          row data + the usage entry already in the page's `usage`
          map, so the dialog is instant. Token plaintext is NOT
          shown; the issuer keeps no copy after Issue. */}
      <Dialog
        open={!!detailsTarget}
        onOpenChange={(o) => !o && setDetailsTarget(null)}
      >
        {/* Same Tailwind-merge pitfall as the Issue dialog — shadcn's
            base classes ship `sm:max-w-sm`, so the override must be at
            the same responsive breakpoint to win. 5xl ≈ 1024px, wide
            enough for the two-column section layout below. */}
        <DialogContent className="max-w-[calc(100%-2rem)] sm:max-w-5xl">
          {detailsTarget && (
            <>
              <DialogHeader>
                <DialogTitle>Capability details</DialogTitle>
                <DialogDescription>
                  Full record as stored on the admin plane.
                </DialogDescription>
              </DialogHeader>
              <DetailsBody
                cap={detailsTarget}
                usageEntry={usage.get(detailsTarget.id)}
              />
              <DialogFooter>
                <Button
                  variant="outline"
                  onClick={() => {
                    void copyToClipboard(detailsTarget.id);
                    showNotification({
                      type: "success",
                      title: "Copied",
                      message: "Capability ID copied.",
                    });
                  }}
                >
                  <ClipboardDocumentIcon className="size-4" />
                  Copy ID
                </Button>
                <Button onClick={() => setDetailsTarget(null)}>Close</Button>
              </DialogFooter>
            </>
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
