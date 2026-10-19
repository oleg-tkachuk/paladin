"use client";

// The menu a list's toolbar carries inside a TableScope: how tightly its
// rows are set, and the sort it opens with — the one on screen, saved.
import { AdjustmentsHorizontalIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  DEFAULT_DENSITY,
  DENSITIES,
  type Density,
} from "@/lib/preferences/tablePrefs";

import { useTableScope } from "./TableScope";

const DENSITY_LABEL: Record<Density, string> = {
  comfortable: "Comfortable",
  compact: "Compact",
};

export function TablePrefsMenu() {
  const scope = useTableScope();
  if (!scope) return null;
  const { prefs, sort } = scope;
  const sortable = sort?.column && sort.direction;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="icon" aria-label="Table settings">
          <AdjustmentsHorizontalIcon className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel>Density</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={prefs.density ?? DEFAULT_DENSITY}
          onValueChange={(v) => void scope.setDensity(v as Density)}
        >
          {DENSITIES.map((d) => (
            <DropdownMenuRadioItem key={d} value={d}>
              {DENSITY_LABEL[d]}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>Default sort</DropdownMenuLabel>
        <DropdownMenuItem
          disabled={!sortable}
          onSelect={() =>
            sortable &&
            void scope.saveSort({
              column: sort!.column!,
              direction: sort!.direction!,
            })
          }
        >
          Open with this sort
        </DropdownMenuItem>
        <DropdownMenuItem
          disabled={!prefs.sort}
          onSelect={() => void scope.clearSort()}
        >
          {prefs.sort
            ? `Clear (${prefs.sort.column}, ${prefs.sort.direction})`
            : "No default sort"}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
