"use client";

import type { ReactNode } from "react";
import { XMarkIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { CLEAR_SIDEBAR } from "@/components/layout/sidebarWidth";
import { cn } from "@/lib/utils";

interface SelectionBarProps {
  // Rows selected; the bar is not rendered while it is zero.
  count: number;
  onClear: () => void;
  // The bar's actions, as small buttons.
  children: ReactNode;
}

// The floating bar a table shows while rows are selected: how many, what can
// be done to them, and a way to drop the selection. One row of ordinary small
// buttons, so it covers as little of the table as it can.
//
// It sits below the object inspector (z-60) and every dialog, so it never
// covers either, and is centred on the content column rather than the window,
// so on a narrow desktop it does not run under the sidebar.
export function SelectionBar({ count, onClear, children }: SelectionBarProps) {
  if (count === 0) return null;
  return (
    <div
      className={cn(
        "pointer-events-none fixed inset-x-0 bottom-4 z-50 flex justify-center px-4",
        CLEAR_SIDEBAR,
      )}
    >
      <div
        role="toolbar"
        aria-label="Selection actions"
        className="pointer-events-auto flex max-w-full items-center gap-1 overflow-x-auto rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-floating"
      >
        <span className="px-2 text-sm font-medium whitespace-nowrap tabular-nums">
          {count} selected
        </span>
        <Separator orientation="vertical" className="my-1" />
        {children}
        <Separator orientation="vertical" className="my-1" />
        <Button
          size="icon-sm"
          variant="ghost"
          onClick={onClear}
          aria-label="Clear selection"
          title="Clear selection"
        >
          <XMarkIcon />
        </Button>
      </div>
    </div>
  );
}
