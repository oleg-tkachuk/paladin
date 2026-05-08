"use client";

import * as React from "react";
import { Switch as SwitchPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

/**
 * Switch — coloured primary on, neutral off.
 *
 * Radix exposes `data-state="checked"` / `data-state="unchecked"`. The
 * shipped shadcn template referenced `data-checked:` / `data-unchecked:`
 * selectors which never matched — both states looked identical. Fixed to
 * use `data-[state=checked]:` / `data-[state=unchecked]:`.
 */
function Switch({
  className,
  size = "default",
  ...props
}: React.ComponentProps<typeof SwitchPrimitive.Root> & {
  size?: "sm" | "default";
}) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      data-size={size}
      className={cn(
        // Layout + base
        "peer group/switch relative inline-flex shrink-0 items-center rounded-full border border-transparent transition-colors outline-none after:absolute after:-inset-x-3 after:-inset-y-2",
        // Sizes
        "data-[size=default]:h-5 data-[size=default]:w-9 data-[size=sm]:h-4 data-[size=sm]:w-7",
        // Focus visible
        "focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/60",
        // Invalid
        "aria-invalid:border-destructive aria-invalid:ring-2 aria-invalid:ring-destructive/30",
        // States — primary tint on, neutral off
        "data-[state=checked]:bg-primary",
        "data-[state=unchecked]:bg-input data-[state=unchecked]:ring-1 data-[state=unchecked]:ring-border dark:data-[state=unchecked]:bg-input/80",
        // Disabled
        "data-disabled:cursor-not-allowed data-disabled:opacity-50",
        className,
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className={cn(
          "pointer-events-none block rounded-full bg-background shadow-sm ring-0 transition-transform",
          "group-data-[size=default]/switch:size-4 group-data-[size=sm]/switch:size-3",
          "group-data-[size=default]/switch:data-[state=checked]:translate-x-4 group-data-[size=default]/switch:data-[state=unchecked]:translate-x-0.5",
          "group-data-[size=sm]/switch:data-[state=checked]:translate-x-3 group-data-[size=sm]/switch:data-[state=unchecked]:translate-x-0.5",
          "data-[state=checked]:bg-primary-foreground dark:data-[state=checked]:bg-white dark:data-[state=unchecked]:bg-foreground",
        )}
      />
    </SwitchPrimitive.Root>
  );
}

export { Switch };
