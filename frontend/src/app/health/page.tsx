"use client";

import { IconChip, IconTone } from "@/components/ui/IconChip";
import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ChevronRightIcon,
  ExclamationTriangleIcon,
  MinusCircleIcon,
  XCircleIcon,
  ShieldCheckIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useStats } from "@/context/StatsContext";
import { useVisiblePolling } from "@/hooks/useVisiblePolling";
import { LiveToggle } from "@/components/ui/LiveToggle";
import { liveState } from "@/lib/live";

import { cn } from "@/lib/utils";
import { STATUS_TONE, StatusTone } from "@/lib/ui/status";
import { StatusDot, StatusLabel } from "@/components/ui/StatusIcon";
import { StatusBar, type StatusSegment } from "@/components/ui/StatusBar";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/Skeleton";
import { formatTime } from "@/lib/format/locale";
import {
  countByStatus,
  needsAttention,
  splitDatabase,
  type Component,
  type Finding,
  type ComponentStatus,
  type Control,
  type Detail,
  detailLines,
  type HealthAll,
  type RoleStatus,
  type Snapshot,
} from "@/lib/health";

/** How often the health page asks again while Live. */
const HEALTH_POLL_MS = 15_000;

// Role render order. Operators read top-down; api/admin first because
// they're the request paths. worker/mcp/dispatcher/ingest are
// background/auxiliary and land below the fold on narrow viewports.
// New roles default to alphabetical order at the bottom; pinning
// them here puts dispatcher and ingest near worker so the
// outbox/storage event-pipeline trio reads as a group.
const ROLE_ORDER = [
  "api",
  "admin",
  "worker",
  "dispatcher",
  "ingest",
  "mcp",
] as const;

// ─── Status meta ────────────────────────────────────────────────────────────

type StateKey = ComponentStatus | "unknown";

// Each state's words, its tone in the shared status vocabulary (its ink and
// dot) and the chip its card title wears. Disabled is a pause an operator
// chose, not a fault.
const META: Record<
  StateKey,
  {
    label: string;
    icon: React.ElementType;
    status: StatusTone;
    tone: IconTone;
  }
> = {
  healthy: {
    label: "Healthy",
    icon: CheckCircleIcon,
    status: StatusTone.Success,
    tone: IconTone.Success,
  },
  degraded: {
    label: "Degraded",
    icon: ExclamationTriangleIcon,
    status: StatusTone.Warning,
    tone: IconTone.Warning,
  },
  unhealthy: {
    label: "Unhealthy",
    icon: XCircleIcon,
    status: StatusTone.Danger,
    tone: IconTone.Danger,
  },
  disabled: {
    label: "Disabled",
    icon: MinusCircleIcon,
    status: StatusTone.Suspended,
    tone: IconTone.Neutral,
  },
  unknown: {
    label: "Unknown",
    icon: ShieldCheckIcon,
    status: StatusTone.Unknown,
    tone: IconTone.Neutral,
  },
};

// rollupOf collapses a list of statuses with the same asymmetric rule
// the backend applies on /readyz: any *critical* unhealthy → unhealthy;
// any non-critical unhealthy or any degraded → degraded; else healthy.
function rollupOf(snaps: Snapshot[]): StateKey {
  if (snaps.length === 0) return "unknown";
  let worst: RoleStatus = "healthy";
  for (const s of snaps) {
    if (s.status === "unhealthy") return "unhealthy";
    if (s.status === "degraded") worst = "degraded";
  }
  return worst;
}

/**
 * How a set of components or roles splits by state, for a StatusBar: the
 * states in META's order, each in its tone.
 */
function segmentsOf(states: readonly StateKey[]): StatusSegment[] {
  return (Object.keys(META) as StateKey[]).map((k) => ({
    tone: META[k].status,
    label: META[k].label.toLowerCase(),
    count: states.filter((s) => s === k).length,
  }));
}

function StatusPill({ status }: { status: StateKey }) {
  const m = META[status];
  return <StatusLabel tone={m.status}>{m.label}</StatusLabel>;
}

// ─── Component row — one per dependency ─────────────────────────────────────
//
// Two-line shape:
//   line 1: dot • name • req-badge • status pill • latency
//   line 2: message — truncated to 1 line
//
// A message that does not fit its line (a timeout, a stack trace) gets a
// chevron that reveals the whole of it in a pre-formatted block; one that
// fits gets none. A disabled component has no row: it is never probed, and
// its card lists it on one "Off" line, why it is off in its tooltip.

// The badge of a component that can be switched off, and what switches it.
// A component with no switch (always_on) carries none.
const CONTROL_BADGE: Partial<
  Record<Control, { label: string; title: string }>
> = {
  config: { label: "config", title: "Switched by a configuration key" },
  database: {
    label: "db",
    title: "In use while something stored in the database uses it",
  },
};

function ComponentRow({ c }: { c: Component }) {
  const [open, setOpen] = useState(false);
  const m = META[c.status];
  const preview = useRef<HTMLParagraphElement>(null);
  const cutOff = useCutOff(preview, c.message);
  // A healthy component's details wait behind the toggle, so a card of
  // healthy rows stays one line each; one in trouble shows them at once.
  const hasDetails = !!c.details?.length;
  const healthy = c.status === "healthy";
  const showDetails = hasDetails && (open || !healthy);
  // Expanding is only offered when it reveals something: details held back,
  // or a message the preview cut off. A toggle that shows the same again is
  // noise.
  const expandable =
    (hasDetails && healthy) || (!!c.message && (cutOff || open));
  const control = c.control && CONTROL_BADGE[c.control];

  return (
    <div className="border-b border-border/40 py-2 last:border-b-0">
      <button
        type="button"
        onClick={() => expandable && setOpen((v) => !v)}
        aria-expanded={expandable ? open : undefined}
        className={cn(
          "flex w-full items-center justify-between gap-3 text-left",
          expandable ? "cursor-pointer hover:opacity-80" : "cursor-default",
        )}
      >
        <div className="flex min-w-0 items-center gap-2">
          <StatusDot tone={m.status} />
          <span
            className="truncate font-mono text-sm"
            title={control ? control.title : undefined}
          >
            {c.name}
          </span>
          {/* Nearly every component is required, so the exception is
              what is marked: one the role keeps serving without. */}
          {!c.critical && (
            <Badge
              variant="outline"
              title="The role keeps serving while it is down"
              className="shrink-0 px-1.5 py-0 font-normal text-muted-foreground"
            >
              optional
            </Badge>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <span
            className={cn("text-xs font-medium", STATUS_TONE[m.status].ink)}
            aria-label={`status: ${c.status}`}
          >
            {c.statusLabel ?? m.label}
          </span>
          <span className="font-mono text-xs tabular-nums text-muted-foreground">
            {c.latency_ms}ms
          </span>
          {expandable && (
            <ChevronRightIcon
              aria-hidden
              className={cn(
                "size-4 shrink-0 text-muted-foreground transition-transform",
                open && "rotate-90",
              )}
            />
          )}
        </div>
      </button>

      {c.message && !open && (
        // Collapsed preview: one line, truncated when it does not fit, with
        // the full text in the tooltip.
        <p
          ref={preview}
          className="mt-1 truncate text-xs text-muted-foreground"
          title={c.message}
        >
          {c.message}
        </p>
      )}

      {c.message && open && (
        // Expanded view: pre-formatted code block so timeouts /
        // multi-line stack traces wrap and stay readable. `whitespace-
        // pre-wrap` preserves newlines; `break-all` catches long
        // tokenless strings (DSNs, JWTs).
        <pre
          className={cn(
            "mt-2 overflow-x-auto rounded-md bg-muted/60 px-3 py-2 text-xs",
            "whitespace-pre-wrap break-all font-mono leading-relaxed",
          )}
        >
          {c.message}
        </pre>
      )}

      {showDetails && <DetailList details={c.details!} />}
    </div>
  );
}

// DetailList shows a component's details on their own line, apart from the
// message: they are not an error, and folding them into the preview would
// offer to expand them. Details about several roles — each role's pool — are
// a table, a role to a column, so the same fact lines up across roles.
function DetailList({ details }: { details: Detail[] }) {
  if (details.some((d) => d.role)) return <DetailTable details={details} />;
  return (
    <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
      {detailLines(details).map((line) => (
        <div key={line.label} className="contents">
          <dt className="text-muted-foreground first-letter:uppercase">
            {line.label}
          </dt>
          <dd className="flex flex-wrap gap-x-1.5">
            {line.facts.map((f, i) => (
              <span key={f.name || i} className="whitespace-nowrap">
                {i > 0 && (
                  <span aria-hidden className="pr-1.5 text-muted-foreground">
                    ·
                  </span>
                )}
                {f.name && (
                  <span className="text-muted-foreground">{f.name} </span>
                )}
                <span className="font-mono tabular-nums">{f.value}</span>
              </span>
            ))}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function DetailTable({ details }: { details: Detail[] }) {
  const roles = unique(details.map((d) => d.role ?? ""));
  const names = unique(details.map((d) => d.name));
  const value = (name: string, role: string) =>
    details.find((d) => d.name === name && (d.role ?? "") === role)?.value ??
    "—";
  return (
    <div className="mt-2 overflow-x-auto">
      <table className="w-full text-xs">
        <thead>
          <tr className="text-muted-foreground">
            <th className="pb-1 text-left font-normal" />
            {roles.map((r) => (
              <th
                key={r}
                scope="col"
                className="pb-1 pl-3 text-right font-mono font-normal"
              >
                {r}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {names.map((n) => (
            <tr key={n} className="border-t border-border/40">
              <th
                scope="row"
                className="py-1 text-left font-normal text-muted-foreground"
              >
                {n}
              </th>
              {roles.map((r) => (
                <td
                  key={r}
                  className="py-1 pl-3 text-right font-mono tabular-nums"
                >
                  {value(n, r)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function unique(xs: string[]): string[] {
  return Array.from(new Set(xs));
}

// useCutOff is whether the one-line preview hides part of message: it runs
// past the line, or has more than one. Re-measured as the card resizes.
function useCutOff(
  ref: React.RefObject<HTMLElement | null>,
  message: string | undefined,
): boolean {
  const [cutOff, setCutOff] = useState(false);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || !message) return;
    const measure = () =>
      setCutOff(message.includes("\n") || el.scrollWidth > el.clientWidth);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref, message]);
  return cutOff;
}

// The role cards' grid: as many columns as fit cards no narrower than their
// widest row, so no component name is cut off; one column when even one card
// does not fit. 23rem holds the widest row there is, `iam_listener` with its
// `required` badge, `Unhealthy` and a four-digit latency (321px), plus the
// card's padding. min(100%, …) keeps a phone-width page to one card that fits.
const ROLE_GRID =
  "grid gap-3 grid-cols-[repeat(auto-fit,minmax(min(100%,23rem),1fr))]";

// ─── Role card — one per backend role ───────────────────────────────────────

// tally counts the healthy components among those probed: a disabled one is
// neither, so it is left out of both.
function tally(components: Component[]): { ok: number; total: number } {
  const probed = components.filter((c) => c.status !== "disabled");
  return {
    ok: probed.filter((c) => c.status === "healthy").length,
    total: probed.length,
  };
}

function RoleCard({ snap }: { snap: Snapshot }) {
  const m = META[snap.status];
  const { ok, total } = tally(snap.components);
  const active = snap.components.filter((c) => c.status !== "disabled");
  const off = snap.components.filter((c) => c.status === "disabled");
  return (
    // gap-0 py-0: the header and the rows carry their own padding, and the
    // card's default gap stacked on it left a blank band under the header.
    <Card className="flex flex-col gap-0 py-0">
      <CardHeader className="flex flex-row items-center justify-between gap-2 px-4 py-3">
        <div className="flex items-center gap-2 min-w-0">
          <IconChip icon={m.icon} tone={m.tone} size="md" />
          <CardTitle className="font-mono text-base">{snap.role}</CardTitle>
        </div>
        <div className="flex items-center gap-2">
          <span className="font-mono text-xs tabular-nums text-muted-foreground">
            {ok}/{total}
          </span>
          <StatusPill status={snap.status} />
        </div>
      </CardHeader>
      <Separator />
      <CardContent className="flex-1 px-4 py-2">
        {snap.components.length === 0 ? (
          <p className="py-3 text-sm text-muted-foreground">
            No checks registered.
          </p>
        ) : (
          <div className="space-y-0">
            {active.map((c) => (
              <ComponentRow key={c.name} c={c} />
            ))}
          </div>
        )}
        {/* Switched off is the expected state of an unused feature: one
            muted line, each name's reason in its tooltip, rather than a row
            each that reads like a check. */}
        {off.length > 0 ? (
          <p className="border-t border-border/40 py-2 text-xs text-muted-foreground">
            <span className="mr-1.5">Off:</span>
            {off.map((c, i) => (
              <span key={c.name}>
                {i > 0 ? ", " : null}
                <span
                  className="font-mono"
                  title={[
                    c.message,
                    c.control && CONTROL_BADGE[c.control]?.title,
                  ]
                    .filter(Boolean)
                    .join(" — ")}
                >
                  {c.name}
                </span>
              </span>
            ))}
          </p>
        ) : null}
      </CardContent>
    </Card>
  );
}

// ─── Needs attention — every fault, ahead of the cards ──────────────────────

/**
 * The components that are down or slow, across every card, the worst first:
 * a fault is read here rather than found by scrolling through the cards.
 */
function AttentionCard({ findings }: { findings: Finding[] }) {
  return (
    <Card className="gap-0 border-warning/40 py-0">
      <CardHeader className="flex flex-row items-center gap-2 px-4 py-3">
        <IconChip
          icon={ExclamationTriangleIcon}
          tone={IconTone.Warning}
          size="md"
        />
        <CardTitle className="text-base">
          Needs attention{" "}
          <span className="font-mono text-sm text-muted-foreground">
            {findings.length}
          </span>
        </CardTitle>
      </CardHeader>
      <Separator />
      <CardContent className="px-4 py-1">
        <ul>
          {findings.map(({ role, component: c }) => {
            const m = META[c.status];
            return (
              <li
                key={`${role}/${c.name}`}
                className="flex items-start gap-2 border-b border-border/40 py-2 last:border-b-0"
              >
                <StatusDot tone={m.status} className="mt-1.5" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                    <span className="font-mono text-sm">
                      <span className="text-muted-foreground">{role} ·</span>{" "}
                      {c.name}
                    </span>
                    {c.critical ? (
                      <Badge
                        variant="outline"
                        className="px-1.5 py-0 font-normal"
                      >
                        required
                      </Badge>
                    ) : null}
                    <span
                      className={cn(
                        "text-xs font-medium",
                        STATUS_TONE[m.status].ink,
                      )}
                    >
                      {c.statusLabel ?? m.label}
                    </span>
                  </div>
                  {c.message ? (
                    <p
                      className="truncate text-xs text-muted-foreground"
                      title={c.message}
                    >
                      {c.message}
                    </p>
                  ) : null}
                </div>
              </li>
            );
          })}
        </ul>
      </CardContent>
    </Card>
  );
}

// ─── Page ───────────────────────────────────────────────────────────────────

export default function HealthPage() {
  const { stats } = useStats(); // version info from api role's GetVersion
  const [data, setData] = useState<HealthAll | null>(null);
  const [loading, setLoading] = useState(true);
  const [lastSync, setLastSync] = useState<Date | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch("/api/health/all", { cache: "no-store" });
      const body = (await res.json()) as HealthAll;
      setData(body);
      setLastSync(new Date());
    } catch {
      // Leave previous data on screen so a transient BFF blip doesn't
      // wipe the dashboard. The next interval tick retries.
    } finally {
      setLoading(false);
    }
  }, []);

  // Auto-refresh every HEALTH_POLL_MS — short enough that a degrading dep
  // shows up quickly, long enough that the page isn't a hot loop. Paused
  // while the tab is hidden, or when Live is switched off.
  const [live, setLive] = useState(true);
  useVisiblePolling(refresh, HEALTH_POLL_MS, live);

  const orderedRoles = useMemo(() => {
    if (!data) return [];
    const byName = new Map(data.roles.map((r) => [r.role, r] as const));
    const out: Snapshot[] = [];
    for (const r of ROLE_ORDER) {
      const s = byName.get(r);
      if (s) {
        out.push(s);
        byName.delete(r);
      }
    }
    // Surface any unknown roles after the canonical four — the backend
    // may have grown a new role and we want to see it.
    for (const k of Array.from(byName.keys()).sort()) {
      out.push(byName.get(k)!);
    }
    return out;
  }, [data]);

  // The database card gathers what every role reported of the database;
  // each role card keeps one row saying whether that role reaches it.
  const { roles: roleCards, database } = useMemo(
    () => splitDatabase(orderedRoles),
    [orderedRoles],
  );
  const cards = useMemo(
    () => (database ? [database, ...roleCards] : roleCards),
    [database, roleCards],
  );

  const rollup = rollupOf(orderedRoles);
  const counts = countByStatus(cards.flatMap((s) => s.components));
  const probed = counts.healthy + counts.degraded + counts.unhealthy;
  const findings = useMemo(() => needsAttention(cards), [cards]);

  const backendVersion = stats?.version?.version || "—";
  const backendCommit = stats?.version?.commit
    ? stats.version.commit.slice(0, 10)
    : "—";
  // UI bundle's own version + commit, baked at build time via the
  // NEXT_PUBLIC_UI_* env vars next.config.ts plumbs in. Operators
  // care about both halves on /health: a stale browser tab against
  // a newer backend (or the inverse during a half-rolled deploy)
  // is the most common "why does the UI look weird" surface, and
  // the two values side-by-side answer it without checking
  // image tags in the cluster.
  const uiVersion = process.env.NEXT_PUBLIC_UI_VERSION || "local";
  const uiCommit =
    (process.env.NEXT_PUBLIC_UI_COMMIT || "").slice(0, 10) || "—";
  const lastSyncLabel = lastSync ? formatTime(lastSync) : "—";

  return (
    <div className="space-y-4">
      <PageHeader
        title="Health & Diagnostics"
        description="Per-role component status across the control plane."
        showDefaultActions={false}
        actions={
          <>
            <LiveToggle
              state={liveState({ live })}
              onToggle={() => setLive((v) => !v)}
              intervalMs={HEALTH_POLL_MS}
            />
            <Button
              variant="outline"
              size="icon"
              onClick={refresh}
              aria-label="Refresh"
            >
              <ArrowPathIcon
                className={cn("size-4", loading && "animate-spin")}
              />
            </Button>
          </>
        }
      />

      {/* ─── Top strip: rollup + counts + version. Single row, no padding
              waste. Replaces the previous grid of 6 cards. ─────────────── */}
      <Card>
        <CardContent className="flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-3">
          <div>
            <div className="text-xs uppercase tracking-wider text-muted-foreground">
              Status
            </div>
            <StatusPill status={rollup} />
          </div>
          <Separator orientation="vertical" className="h-8" />
          {/* Of the checks that run: a switched-off one is neither healthy
              nor failing, and the bar beside counts it apart. */}
          <div>
            <div className="text-xs uppercase tracking-wider text-muted-foreground">
              Healthy checks
            </div>
            <div className="font-mono text-base tabular-nums">
              {probed === 0 ? "—" : `${counts.healthy}/${probed}`}
            </div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-wider text-muted-foreground">
              Roles
            </div>
            <div className="font-mono text-base tabular-nums">
              {orderedRoles.length}
            </div>
          </div>
          {/* How the components split by state, after Argo CD's status
              bar: a fault among many shows before the rollup turns. */}
          <StatusBar
            segments={segmentsOf(
              cards.flatMap((s) => s.components.map((c) => c.status)),
            )}
            className="w-64 max-w-full"
          />
          <Separator orientation="vertical" className="h-8" />
          {/* Backend + UI build-info as two stacked label/value pairs.
              Stacked rather than four side-by-side cells so the
              relationship "this is the backend" / "this is the UI"
              reads vertically and a skew jumps out at a glance. */}
          <div className="flex flex-col gap-0.5 text-xs">
            <div className="flex items-center gap-2">
              <span className="w-14 uppercase tracking-wider text-muted-foreground">
                backend
              </span>
              <span className="font-mono">{backendVersion}</span>
              <span className="text-muted-foreground">·</span>
              <span className="font-mono text-muted-foreground">
                {backendCommit}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <span className="w-14 uppercase tracking-wider text-muted-foreground">
                ui
              </span>
              <span className="font-mono">{uiVersion}</span>
              <span className="text-muted-foreground">·</span>
              <span className="font-mono text-muted-foreground">
                {uiCommit}
              </span>
            </div>
          </div>
          <div className="ml-auto text-right">
            <div className="text-xs uppercase tracking-wider text-muted-foreground">
              Last sync
            </div>
            <div className="font-mono text-base">{lastSyncLabel}</div>
          </div>
        </CardContent>
      </Card>

      {findings.length > 0 ? <AttentionCard findings={findings} /> : null}

      {/* ─── Per-role cards, as many to a row as fit whole (ROLE_GRID). */}
      {!data && loading ? (
        <div className={ROLE_GRID}>
          {ROLE_ORDER.map((r) => (
            <Skeleton key={r} className="h-45 rounded-xl" />
          ))}
        </div>
      ) : orderedRoles.length === 0 ? (
        <Card>
          <CardContent className="py-10 text-center text-sm text-muted-foreground">
            No roles reported. Check PALADIN_*_URL env vars on the BFF.
          </CardContent>
        </Card>
      ) : (
        <div className={ROLE_GRID}>
          {cards.map((s) => (
            <RoleCard key={s.role} snap={s} />
          ))}
        </div>
      )}
    </div>
  );
}
