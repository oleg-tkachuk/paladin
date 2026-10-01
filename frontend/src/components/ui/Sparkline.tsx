"use client";

import React, { useId } from "react";
import { cn } from "@/lib/utils";

interface SparklineProps {
  data: number[];
  /** Colour comes from the text colour (`currentColor`); `text-primary` unless set. */
  className?: string;
}

const WIDTH = 100;
const HEIGHT = 30;

export const Sparkline = React.memo(({ data, className }: SparklineProps) => {
  // One id per instance: two sparklines sharing a gradient id would both
  // paint with whichever definition the document found first.
  const gradientId = useId();
  if (!data.length) return null;

  const max = Math.max(...data);
  const min = Math.min(...data);
  const range = max - min || 1;

  const points = data.map((d, i) => ({
    x: (i / Math.max(data.length - 1, 1)) * WIDTH,
    y: HEIGHT - ((d - min) / range) * HEIGHT,
  }));

  const pathData = `M ${points.map((p) => `${p.x},${p.y}`).join(" L ")}`;
  const areaData = `${pathData} L ${WIDTH},${HEIGHT} L 0,${HEIGHT} Z`;

  return (
    <svg
      viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
      className={cn("w-full h-10 overflow-visible text-primary", className)}
      preserveAspectRatio="none"
    >
      <defs>
        <linearGradient id={gradientId} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0%" stopColor="currentColor" stopOpacity="0.2" />
          <stop offset="100%" stopColor="currentColor" stopOpacity="0" />
        </linearGradient>
      </defs>
      <path
        d={areaData}
        fill={`url(#${gradientId})`}
        className="transition-all duration-1000"
      />
      <path
        d={pathData}
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        className="transition-all duration-1000"
      />
    </svg>
  );
});

Sparkline.displayName = "Sparkline";
