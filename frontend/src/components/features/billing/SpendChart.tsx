"use client";

// SpendChart — a tenant's spend per time bucket, as bars on an even time axis.
//
// One series, so no legend: the card's title names it. The value axis starts
// at zero and tops out at a round number, with a recessive grid; each bar
// answers hover and focus with its bucket, spend and charge count. A table of
// the same points sits behind it for screen readers.

import { useEffect, useId, useRef, useState } from "react";

import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { formatCount, formatDate, formatDateTime } from "@/lib/format/locale";

import { niceMax, type Granularity, type SpendPoint } from "./spendSeries";

// Drawn in CSS pixels at the container's measured width, so axis text stays
// its real size on a phone instead of shrinking with a scaled viewBox.
// DEFAULT_WIDTH is the first frame's, before the container is measured.
const DEFAULT_WIDTH = 720;
const HEIGHT = 180;
const AXIS_GUTTER = 56;
const BASELINE_PAD = 20;
const TOP_PAD = 8;
// The surface gap between adjacent bars, and the bars' rounded data-end.
const BAR_GAP = 2;
const BAR_RADIUS = 4;
// Bars thinner than this skip their rounding: a rounded 1px bar is a dot.
const MIN_ROUNDED_WIDTH = BAR_RADIUS * 2;
// The value axis: 0, half, top.
const GRID_FRACTIONS = [0, 0.5, 1];
// One date label per this many pixels at most, first and last always.
const PX_PER_X_LABEL = 96;

export interface SpendChartProps {
  points: SpendPoint[];
  granularity: Granularity;
  /** Writes a value for the axis and the tooltip. */
  formatValue: (value: number) => string;
  /** Writes a point's spend exactly, for the tooltip; defaults to formatValue. */
  formatPoint?: (point: SpendPoint) => string;
  className?: string;
}

function bucketLabel(start: Date, granularity: Granularity): string {
  if (granularity === "hour") return formatDateTime(start);
  if (granularity === "week") return `Week of ${formatDate(start)}`;
  return formatDate(start);
}

// A bar whose top corners are rounded and whose foot sits square on the axis.
function barPath(x: number, y: number, w: number, h: number): string {
  const r = w >= MIN_ROUNDED_WIDTH ? Math.min(BAR_RADIUS, h) : 0;
  const bottom = y + h;
  return [
    `M ${x} ${bottom}`,
    `L ${x} ${y + r}`,
    `Q ${x} ${y} ${x + r} ${y}`,
    `L ${x + w - r} ${y}`,
    `Q ${x + w} ${y} ${x + w} ${y + r}`,
    `L ${x + w} ${bottom}`,
    "Z",
  ].join(" ");
}

export function SpendChart({
  points,
  granularity,
  formatValue,
  formatPoint,
  className,
}: SpendChartProps) {
  const [active, setActive] = useState<number | null>(null);
  const tableId = useId();
  const box = useRef<HTMLDivElement>(null);
  const [WIDTH, setWidth] = useState(DEFAULT_WIDTH);
  useEffect(() => {
    const el = box.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(([entry]) => {
      const w = Math.round(entry.contentRect.width);
      if (w > 0) setWidth(w);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  const top = niceMax(Math.max(0, ...points.map((p) => p.value)));
  const plotW = WIDTH - AXIS_GUTTER;
  const plotH = HEIGHT - BASELINE_PAD - TOP_PAD;
  const slot = points.length > 0 ? plotW / points.length : plotW;
  const barW = Math.max(1, slot - BAR_GAP);
  const y = (v: number) => TOP_PAD + plotH - (v / top) * plotH;
  const maxLabels = Math.max(2, Math.floor(WIDTH / PX_PER_X_LABEL));
  const labelEvery = Math.max(1, Math.ceil(points.length / maxLabels));
  const shown = active !== null ? points[active] : null;
  const pointText = formatPoint ?? ((p: SpendPoint) => formatValue(p.value));

  return (
    <div ref={box} className={cn("relative", className)}>
      <svg
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        width="100%"
        height={HEIGHT}
        className="overflow-visible"
        role="img"
        aria-describedby={tableId}
        onMouseLeave={() => setActive(null)}
      >
        {GRID_FRACTIONS.map((f) => {
          const gy = y(top * f);
          return (
            <g key={f}>
              <line
                x1={AXIS_GUTTER}
                x2={WIDTH}
                y1={gy}
                y2={gy}
                className={f === 0 ? "stroke-border" : "stroke-border/50"}
                strokeDasharray={f === 0 ? undefined : "2 4"}
              />
              <text
                x={AXIS_GUTTER - 8}
                y={gy}
                textAnchor="end"
                dominantBaseline="middle"
                className="fill-muted-foreground text-xs tabular-nums"
              >
                {formatValue(top * f)}
              </text>
            </g>
          );
        })}
        {points.map((p, i) => {
          const x = AXIS_GUTTER + i * slot + BAR_GAP / 2;
          const h = Math.max(0, y(0) - y(p.value));
          const isActive = i === active;
          return (
            <g key={p.start.getTime()}>
              {/* The hit target is the whole column, not the bar. */}
              <rect
                x={AXIS_GUTTER + i * slot}
                y={TOP_PAD}
                width={slot}
                height={plotH}
                className={isActive ? "fill-muted/40" : "fill-transparent"}
                onMouseEnter={() => setActive(i)}
                onFocus={() => setActive(i)}
                onBlur={() => setActive(null)}
                tabIndex={p.value > 0 ? 0 : -1}
                aria-label={`${bucketLabel(p.start, granularity)}: ${pointText(p)}`}
              />
              {h > 0 && (
                <path
                  d={barPath(x, y(p.value), barW, h)}
                  className={cn(
                    "pointer-events-none transition-colors",
                    isActive ? "fill-primary" : "fill-primary/75",
                  )}
                />
              )}
              {(i % labelEvery === 0 || i === points.length - 1) && (
                <text
                  x={x + barW / 2}
                  y={HEIGHT - 4}
                  textAnchor="middle"
                  className="fill-muted-foreground text-xs"
                >
                  {granularity === "hour"
                    ? formatDateTime(p.start).slice(0, -3)
                    : formatDate(p.start).replace(/ \d{4}$/, "")}
                </text>
              )}
            </g>
          );
        })}
      </svg>

      {shown && (
        <div
          className="pointer-events-none absolute top-0 z-10 -translate-x-1/2 rounded-md border bg-popover px-3 py-2 text-xs shadow-floating"
          style={{
            left: `${((AXIS_GUTTER + (active! + 0.5) * slot) / WIDTH) * 100}%`,
          }}
          role="status"
        >
          <p className="font-medium">{bucketLabel(shown.start, granularity)}</p>
          <p className="tabular-nums">{pointText(shown)}</p>
          <p className={cn(T.hint, "tabular-nums")}>
            {formatCount(shown.chargeCount)}{" "}
            {shown.chargeCount === 1 ? "charge" : "charges"}
          </p>
        </div>
      )}

      <table id={tableId} className="sr-only">
        <caption>Spend per {granularity}</caption>
        <thead>
          <tr>
            <th scope="col">Period</th>
            <th scope="col">Spend</th>
            <th scope="col">Charges</th>
          </tr>
        </thead>
        <tbody>
          {points
            .filter((p) => p.chargeCount > 0)
            .map((p) => (
              <tr key={p.start.getTime()}>
                <td>{bucketLabel(p.start, granularity)}</td>
                <td>{pointText(p)}</td>
                <td>{formatCount(p.chargeCount)}</td>
              </tr>
            ))}
        </tbody>
      </table>
    </div>
  );
}
