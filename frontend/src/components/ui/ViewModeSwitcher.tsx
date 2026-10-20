"use client";

// The control that lays a list out as a table, as tiles or as a summary,
// after Argo CD's applications list. Three pressed-or-not buttons rather than
// a menu: the choice is seen at a glance and is one click away.
import type React from "react";
import {
  ChartPieIcon,
  Squares2X2Icon,
  TableCellsIcon,
} from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { VIEW_MODES, type ViewMode } from "@/lib/table/viewMode";

import { useTableScope } from "./TableScope";

const VIEW: Record<ViewMode, { label: string; icon: React.ElementType }> = {
  table: { label: "Table", icon: TableCellsIcon },
  tiles: { label: "Tiles", icon: Squares2X2Icon },
  summary: { label: "Summary", icon: ChartPieIcon },
};

/**
 * Inside a TableScope the choice is also saved as the table's view, so the
 * list opens in it next time; outside one it only changes the page.
 */
export function ViewModeSwitcher({
  value,
  onChange,
}: {
  value: ViewMode;
  onChange: (view: ViewMode) => void;
}) {
  const scope = useTableScope();
  const choose = (view: ViewMode) => {
    onChange(view);
    // A failed save costs the choice on the next visit, nothing more.
    scope?.saveView(view).catch(() => undefined);
  };
  return (
    <div
      role="group"
      aria-label="View"
      className="flex items-center gap-0.5 rounded-lg border bg-background p-0.5"
    >
      {VIEW_MODES.map((mode) => {
        const { label, icon: Icon } = VIEW[mode];
        return (
          <Button
            key={mode}
            type="button"
            size="icon-sm"
            variant={mode === value ? "secondary" : "ghost"}
            aria-label={label}
            title={label}
            aria-pressed={mode === value}
            onClick={() => choose(mode)}
          >
            <Icon className="size-4" />
          </Button>
        );
      })}
    </div>
  );
}
