import * as React from "react";
import { cn } from "@/lib/utils";

interface SkeletonProps extends React.HTMLAttributes<HTMLDivElement> {
  /** Legacy `variant` prop. Maps to a sensible Tailwind shape. */
  variant?: "text" | "rect" | "circle";
  width?: string | number;
  height?: string | number;
}

/**
 * Backward-compatible skeleton that accepts both shadcn's slim API
 * (`<Skeleton className="h-4 w-32" />`) and the legacy
 * (`<Skeleton variant="text" width={120} />`) shape.
 */
function Skeleton({
  className,
  variant,
  width,
  height,
  style,
  ...props
}: SkeletonProps) {
  const variantClass: Record<NonNullable<SkeletonProps["variant"]>, string> = {
    text: "rounded h-4 w-full",
    rect: "rounded-md",
    circle: "rounded-full",
  };

  const inline: React.CSSProperties = { ...style };
  if (width !== undefined)
    inline.width = typeof width === "number" ? `${width}px` : width;
  if (height !== undefined)
    inline.height = typeof height === "number" ? `${height}px` : height;

  return (
    <div
      data-slot="skeleton"
      className={cn(
        "animate-pulse bg-muted",
        variant ? variantClass[variant] : "rounded-md",
        className,
      )}
      style={inline}
      {...props}
    />
  );
}

export { Skeleton };
