"use client";

// RelativeTime — humanised "3 minutes ago" / "in 2 days" with tooltip
// showing the absolute ISO string for precision. Used everywhere
// audit-y timestamps appear: audit log rows, activity timelines, file
// modified-at columns. Replaces ad-hoc `formatDate(ts)` calls that
// produced unscannable wall-of-ISO output.
//
// Re-renders every minute so "1 minute ago" eventually becomes "2
// minutes ago" without a page reload. Cancels its timer on unmount —
// no leaked intervals.

import React, { useEffect, useState } from "react";

import { timestampToDate } from "@/lib/utils";
import { DISPLAY_LOCALE, formatDateTime } from "@/lib/format/locale";

type TimestampInput =
  | Date
  | string
  | number
  // protobuf Timestamp message: { seconds: bigint, nanos: number }
  | { seconds?: bigint | number; nanos?: number }
  | null
  | undefined;

function toDate(v: TimestampInput): Date | null {
  if (v == null) return null;
  if (v instanceof Date) return v;
  if (typeof v === "string") {
    const d = new Date(v);
    return isNaN(d.getTime()) ? null : d;
  }
  if (typeof v === "number") return new Date(v * 1000);
  // Defer to the existing helper for protobuf Timestamp objects
  try {
    return timestampToDate(v as never);
  } catch {
    return null;
  }
}

const UNITS: Array<[Intl.RelativeTimeFormatUnit, number]> = [
  ["year", 365 * 24 * 60 * 60],
  ["month", 30 * 24 * 60 * 60],
  ["week", 7 * 24 * 60 * 60],
  ["day", 24 * 60 * 60],
  ["hour", 60 * 60],
  ["minute", 60],
  ["second", 1],
];

// Module-level: locale config never changes, and constructing
// Intl.RelativeTimeFormat reads locale data — too costly to repeat on
// every render of every table row.
const RTF = new Intl.RelativeTimeFormat(DISPLAY_LOCALE, { numeric: "auto" });

function relativeFormat(d: Date, now: Date): string {
  const diffSecondsRaw = (d.getTime() - now.getTime()) / 1000;
  const absSec = Math.abs(diffSecondsRaw);
  if (absSec < 5) return "just now";
  const sign = diffSecondsRaw < 0 ? -1 : 1;
  for (const [unit, sec] of UNITS) {
    if (absSec >= sec || unit === "second") {
      const value = Math.round(diffSecondsRaw / sec);
      // Intl.RelativeTimeFormat handles plural + sign + locale.
      return RTF.format(value, unit) || `${sign * Math.round(absSec)} ${unit}`;
    }
  }
  return "just now";
}

export interface RelativeTimeProps {
  ts: TimestampInput;
  /** Re-render interval in ms. Defaults to 60_000 (1 min). */
  refreshMs?: number;
  /** Override the className on the surrounding <time>. */
  className?: string;
  /** When true, render absolute ISO instead of relative (for tables
   *  where space allows precision). */
  absolute?: boolean;
}

export function RelativeTime({
  ts,
  refreshMs = 60_000,
  className,
  absolute = false,
}: RelativeTimeProps) {
  const d = toDate(ts);
  const [, setTick] = useState(0);

  // Depend on the epoch value, not the Date object — toDate() returns
  // a fresh object every render, which used to tear down and re-arm
  // the interval on each parent re-render.
  const dMs = d ? d.getTime() : null;
  useEffect(() => {
    if (absolute || dMs === null) return;
    // Skip re-render ticks while the tab is hidden (tables mount many
    // instances of this component); one catch-up tick on return keeps
    // the displayed text honest.
    const tick = () => {
      if (!document.hidden) setTick((t) => t + 1);
    };
    const id = setInterval(tick, refreshMs);
    document.addEventListener("visibilitychange", tick);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [dMs, refreshMs, absolute]);

  if (!d) {
    return (
      <span className={className} title="No timestamp">
        —
      </span>
    );
  }
  const iso = d.toISOString();
  const human = absolute ? formatDateTime(d) : relativeFormat(d, new Date());
  return (
    <time dateTime={iso} title={iso} className={className}>
      {human}
    </time>
  );
}
