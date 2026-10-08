"use client";

import { useStats } from "@/context/StatsContext";
import { Tooltip } from "@/components/ui/Tooltip";
import { cn } from "@/lib/utils";
import {
  type ComponentHealth,
  componentStatusLabel,
} from "@/lib/connect/system";

/**
 * SystemStatus — one calm health affordance for the top bar.
 *
 * The old version was a strip of four raw-Tailwind chips (Live/Err dot,
 * latency, component count, version) that shouted red "ERR" for *any*
 * non-HEALTHY rollup — including a not-yet-loaded "unknown", which is a
 * lie: nothing has failed, we just haven't heard back. Here the default
 * posture is quiet. Colour escalates only when the backend actually
 * reports trouble, and the numeric detail (components, latency) moves
 * into the tooltip so the bar carries a single word, not a dashboard.
 *
 * Colours come from the theme tokens (success / warning / destructive /
 * muted) so the pill stays on-brand instead of importing a second,
 * clashing palette.
 */

type Tone = "live" | "checking" | "unknown" | "degraded" | "down";

const TONE: Record<
  Tone,
  { label: string; dot: string; text: string; pulse: boolean }
> = {
  live: {
    label: "Live",
    dot: "bg-success",
    text: "text-success",
    pulse: true,
  },
  checking: {
    label: "Checking",
    dot: "bg-muted-foreground",
    text: "text-muted-foreground",
    pulse: false,
  },
  degraded: {
    label: "Degraded",
    dot: "bg-warning",
    text: "text-warning",
    pulse: true,
  },
  unknown: {
    // Distinct from "Down": nothing reported trouble — we could not ask.
    // Saying "Down" for an unreachable probe is the same lie in the other
    // direction as saying "Live" for one.
    label: "Unknown",
    dot: "bg-muted-foreground",
    text: "text-muted-foreground",
    pulse: false,
  },
  down: {
    label: "Down",
    dot: "bg-destructive",
    text: "text-destructive",
    pulse: true,
  },
};

function toneFor(rollup: string, loading: boolean): Tone {
  if (loading && !rollup) return "checking";
  switch (rollup) {
    case "HEALTHY":
    case "OK":
      return "live";
    case "DEGRADED":
      return "degraded";
    case "":
    case "UNKNOWN":
    case "UNSPECIFIED":
      return "checking";
    default:
      return "down"; // UNHEALTHY / ERROR / DOWN
  }
}

export function RealTimeStatus() {
  const { stats, loading, healthStatus, healthReason } = useStats();

  const health = stats?.health;
  const rollup =
    health?.status !== undefined
      ? componentStatusLabel(health.status).toUpperCase()
      : "";
  // The shell reports the health section separately from the health it
  // carries: an unreachable probe greys this badge, and the page it sits on
  // renders regardless.
  const tone: Tone =
    healthStatus === "unavailable" ? "unknown" : toneFor(rollup, loading);
  const meta = TONE[tone];

  const components = health?.components ?? [];
  const maxLatency = components.reduce((max: number, dep: ComponentHealth) => {
    const l = Number(dep.latencyMs);
    return Number.isFinite(l) && l > max ? l : max;
  }, 0);

  const detail =
    tone === "unknown"
      ? `Health is unreachable, not failing${healthReason ? ` — ${healthReason}` : ""}.`
      : tone === "checking"
        ? "Waiting for the first health report."
        : `${meta.label} · ${components.length} component${
            components.length === 1 ? "" : "s"
          } reporting · ${maxLatency}ms worst latency`;

  return (
    <Tooltip content={detail}>
      <div
        className={cn(
          "hidden items-center gap-2 rounded-full border border-border/70 bg-muted/30 px-2.5 py-1 sm:inline-flex",
        )}
      >
        <span className="relative flex size-2">
          {meta.pulse && (
            <span
              className={cn(
                "absolute inline-flex size-full animate-ping rounded-full opacity-60",
                meta.dot,
              )}
            />
          )}
          <span
            className={cn("relative inline-flex size-2 rounded-full", meta.dot)}
          />
        </span>
        <span
          className={cn(
            "text-sm font-medium uppercase tracking-wide",
            meta.text,
          )}
        >
          {meta.label}
        </span>
      </div>
    </Tooltip>
  );
}
