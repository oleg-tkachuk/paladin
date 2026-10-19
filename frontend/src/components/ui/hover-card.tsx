"use client";

import * as React from "react";
import { HoverCard as HoverCardPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

/** How long the pointer rests before a card opens, and lingers after. */
export const HOVER_CARD_OPEN_DELAY_MS = 400;
export const HOVER_CARD_CLOSE_DELAY_MS = 150;

function HoverCard(
  props: React.ComponentProps<typeof HoverCardPrimitive.Root>,
) {
  return (
    <HoverCardPrimitive.Root
      data-slot="hover-card"
      openDelay={HOVER_CARD_OPEN_DELAY_MS}
      closeDelay={HOVER_CARD_CLOSE_DELAY_MS}
      {...props}
    />
  );
}

function HoverCardTrigger(
  props: React.ComponentProps<typeof HoverCardPrimitive.Trigger>,
) {
  return (
    <HoverCardPrimitive.Trigger data-slot="hover-card-trigger" {...props} />
  );
}

function HoverCardContent({
  className,
  align = "start",
  sideOffset = 6,
  ...props
}: React.ComponentProps<typeof HoverCardPrimitive.Content>) {
  return (
    <HoverCardPrimitive.Portal>
      <HoverCardPrimitive.Content
        data-slot="hover-card-content"
        align={align}
        sideOffset={sideOffset}
        className={cn(
          "z-50 w-72 rounded-md border bg-popover p-3 text-sm text-popover-foreground shadow-floating outline-hidden",
          className,
        )}
        {...props}
      />
    </HoverCardPrimitive.Portal>
  );
}

export { HoverCard, HoverCardTrigger, HoverCardContent };
