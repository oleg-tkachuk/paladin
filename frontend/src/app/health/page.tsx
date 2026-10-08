"use client";

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
import { cn } from "@/lib/utils";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/Skeleton";
import { formatTime } from "@/lib/format/locale";
import {
  splitDatabase,
  type Component,
  type ComponentStatus,
  type Control,
  type Detail,
  type HealthAll,
  type RoleStatus,
  type Snapshot,
} from "@/lib/health";

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

const META: Record<
  StateKey,
  { label: string; icon: React.ElementType; color: string; dot: string }
> = {
  healthy: {
    label: "Healthy",
    icon: CheckCircleIcon,
    color: "text-chart-2",
    dot: "bg-chart-2",
  },
  degraded: {
    label: "Degraded",
    icon: ExclamationTriangleIcon,
    color: "text-chart-3",
    dot: "bg-chart-3",
  },
  unhealthy: {
    label: "Unhealthy",
    icon: XCircleIcon,
    color: "text-destructive",
    dot: "bg-destructive",
  },
  disabled: {
    label: "Disabled",
    icon: MinusCircleIcon,
    color: "text-muted-foreground",
    dot: "bg-muted-foreground/40",
  },
  unknown: {
    label: "Unknown",
    icon: ShieldCheckIcon,
    color: "text-muted-foreground",
    dot: "bg-muted-foreground",
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

function StatusPill({ status }: { status: StateKey }) {
  const m = META[status];
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-sm font-medium",
        m.color,
      )}
    >
      <span
        className={cn(
          "size-2 rounded-full",
          m.dot,
          status !== "healthy" && "animate-pulse",
        )}
      />
      {m.label}
    </span>
  );
}

// ─── Component row — one per dependency ─────────────────────────────────────
//
// Two-line shape:
//   line 1: dot • name • req-badge • status pill • latency
//   line 2: message — truncated to 1 line
//
// A message that does not fit its line (a timeout, a stack trace) gets a
// chevron that reveals the whole of it in a pre-formatted block; one that
// fits gets none. A disabled component is muted and shows no latency, since
// it is never probed, and why it is off stays behind its chevron: it is the
// expected state, not something to read on every visit.

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
  const disabled = c.status === "disabled";
  const preview = useRef<HTMLParagraphElement>(null);
  const cutOff = useCutOff(preview, c.message);
  // A disabled row always folds its reason away. Otherwise expanding is only
  // offered when the preview does not already show the whole message: a
  // toggle that reveals the same text again is noise.
  const expandable = !!c.message && (disabled || cutOff || open);
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
          <span className={cn("size-2 shrink-0 rounded-full", m.dot)} />
          <span
            className={cn(
              "truncate font-mono text-sm",
              disabled && "text-muted-foreground",
            )}
          >
            {c.name}
          </span>
          {c.critical && !disabled && (
            <Badge
              variant="outline"
              className="shrink-0 px-1.5 py-0 text-sm font-normal"
            >
              required
            </Badge>
          )}
          {control && (
            <Badge
              variant="secondary"
              title={control.title}
              className="shrink-0 px-1.5 py-0 text-sm font-normal"
            >
              {control.label}
            </Badge>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <span
            className={cn("text-xs font-medium", m.color)}
            aria-label={`status: ${c.status}`}
          >
            {c.statusLabel ?? m.label}
          </span>
          {/* A disabled component is not probed: no latency to show. */}
          {!disabled && (
            <span className="font-mono text-xs tabular-nums text-muted-foreground">
              {c.latency_ms}ms
            </span>
          )}
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

      {c.message && !open && !disabled && (
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

      {c.message && open && disabled && (
        <p className="mt-1 text-xs text-muted-foreground">{c.message}</p>
      )}

      {c.message && open && !disabled && (
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

      {!!c.details?.length && <DetailList details={c.details} />}
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
    <dl className="mt-1 flex flex-wrap gap-x-4 gap-y-0.5 text-xs text-muted-foreground">
      {details.map((d) => (
        <div key={d.name} className="flex gap-1.5">
          <dt>{d.name}</dt>
          <dd className="font-mono tabular-nums text-foreground">{d.value}</dd>
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
  const Icon = m.icon;
  const { ok, total } = tally(snap.components);
  return (
    // gap-0 py-0: the header and the rows carry their own padding, and the
    // card's default gap stacked on it left a blank band under the header.
    <Card className="flex flex-col gap-0 py-0">
      <CardHeader className="flex flex-row items-center justify-between gap-2 px-4 py-3">
        <div className="flex items-center gap-2 min-w-0">
          <Icon className={cn("size-5 shrink-0", m.color)} />
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
            {snap.components.map((c) => (
              <ComponentRow key={c.name} c={c} />
            ))}
          </div>
        )}
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

  // Auto-refresh every 15s — short enough that a degrading dep shows
  // up quickly, long enough that the page isn't a hot loop. Paused
  // while the tab is hidden.
  useVisiblePolling(refresh, 15_000);

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
  const cards = database ? [database, ...roleCards] : roleCards;

  const rollup = rollupOf(orderedRoles);
  const RollupIcon = META[rollup].icon;
  const { ok: healthyComponents, total: totalComponents } = tally(
    cards.flatMap((s) => s.components),
  );

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
        }
      />

      {/* ─── Top strip: rollup + counts + version. Single row, no padding
              waste. Replaces the previous grid of 6 cards. ─────────────── */}
      <Card>
        <CardContent className="flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-3">
          <div className="flex items-center gap-2.5">
            <RollupIcon className={cn("size-6", META[rollup].color)} />
            <div>
              <div className="text-xs uppercase tracking-wider text-muted-foreground">
                Rollup
              </div>
              <StatusPill status={rollup} />
            </div>
          </div>
          <Separator orientation="vertical" className="h-8" />
          <div>
            <div className="text-xs uppercase tracking-wider text-muted-foreground">
              Components
            </div>
            <div className="font-mono text-base tabular-nums">
              {totalComponents === 0
                ? "—"
                : `${healthyComponents}/${totalComponents}`}
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
