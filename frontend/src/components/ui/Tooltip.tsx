"use client";

/**
 * Compatibility-layered Tooltip.
 *
 * Two APIs live in this file:
 *
 * 1. shadcn/ui primitives — TooltipRoot, TooltipTrigger, TooltipContent,
 *    TooltipProvider. Preferred for new code.
 *
 * 2. Legacy `<Tooltip content="…" position="top">…</Tooltip>` API —
 *    the default `Tooltip` export, used by existing pages.
 */

import * as React from "react";
import { Tooltip as TooltipPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

function TooltipProvider({
  delayDuration = 150,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Provider>) {
  return (
    <TooltipPrimitive.Provider
      data-slot="tooltip-provider"
      delayDuration={delayDuration}
      {...props}
    />
  );
}

function TooltipRoot(
  props: React.ComponentProps<typeof TooltipPrimitive.Root>,
) {
  return <TooltipPrimitive.Root data-slot="tooltip" {...props} />;
}

function TooltipTrigger(
  props: React.ComponentProps<typeof TooltipPrimitive.Trigger>,
) {
  return <TooltipPrimitive.Trigger data-slot="tooltip-trigger" {...props} />;
}

function TooltipContent({
  className,
  sideOffset = 6,
  children,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Content>) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        data-slot="tooltip-content"
        sideOffset={sideOffset}
        className={cn(
          "z-50 max-w-xs rounded-md bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md ring-1 ring-border",
          className,
        )}
        {...props}
      >
        {children}
      </TooltipPrimitive.Content>
    </TooltipPrimitive.Portal>
  );
}

// ─── legacy Tooltip wrapper ────────────────────────────────────────────────

interface LegacyTooltipProps {
  children: React.ReactNode;
  content: React.ReactNode;
  className?: string;
  position?: "top" | "bottom" | "left" | "right";
}

function Tooltip({
  children,
  content,
  className,
  position = "top",
}: LegacyTooltipProps) {
  return (
    <TooltipRoot>
      <TooltipTrigger asChild>
        <span className={cn("inline-flex", className)}>{children}</span>
      </TooltipTrigger>
      <TooltipContent side={position}>{content}</TooltipContent>
    </TooltipRoot>
  );
}

export {
  Tooltip,
  TooltipRoot,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
};
