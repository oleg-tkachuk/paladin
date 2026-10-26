"use client";

// One facet of a list, as a toolbar button after Argo CD's filter sidebar:
// its name and how many answers are chosen on the button, and in its menu
// every answer with a checkbox, its status dot where it has one, and how many
// rows give it — "10k+" where the count is a lower bound, nothing where none
// is known. Choosing keeps the menu open, so several answers are
// chosen in one visit. Controlled: useTableFacets owns the choices.
import { ChevronDownIcon, FunnelIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Count } from "@/components/ui/Count";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { StatusDot } from "@/components/ui/StatusIcon";
import type { FacetControl } from "@/hooks/useTableFacets";
import { formatCount } from "@/lib/format/locale";
import { FACET_COUNTING, type FacetCounting } from "@/lib/table/facets";
import { cn } from "@/lib/utils";

/** What the menu says when the loaded rows give no answer to offer. */
const NOTHING_TO_CHOOSE = "Nothing to filter by";

/** Follows a count that is a lower bound: "10k+". */
const AT_LEAST_MARK = "+";

/** How an answer reads aloud: "Disabled, 3 rows", or its label alone. */
function optionName(label: string, count: number, counting: FacetCounting) {
  if (counting === FACET_COUNTING.withheld) return label;
  const rows = `${formatCount(count)} ${count === 1 ? "row" : "rows"}`;
  return counting === FACET_COUNTING.lowerBound
    ? `${label}, at least ${rows}`
    : `${label}, ${rows}`;
}

export function FacetFilter({
  label,
  options,
  selected,
  onChange,
  counting = FACET_COUNTING.exact,
}: Omit<FacetControl, "id">) {
  const active = selected.length > 0;
  const toggle = (value: string, on: boolean) =>
    onChange(on ? [...selected, value] : selected.filter((v) => v !== value));
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant={active ? "secondary" : "outline"}
          size="sm"
          aria-label={
            active
              ? `Filter by ${label.toLowerCase()}, ${selected.length} chosen`
              : `Filter by ${label.toLowerCase()}`
          }
        >
          <FunnelIcon className="size-4" />
          {label}
          {active ? (
            <span className="rounded-full bg-primary px-1.5 text-xs tabular-nums text-primary-foreground">
              {selected.length}
            </span>
          ) : null}
          <ChevronDownIcon className="size-3.5 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-60">
        <DropdownMenuLabel>{label}</DropdownMenuLabel>
        {options.length === 0 ? (
          <DropdownMenuItem disabled>{NOTHING_TO_CHOOSE}</DropdownMenuItem>
        ) : (
          options.map((o) => (
            <DropdownMenuCheckboxItem
              key={o.value}
              // Read as "Disabled, 3 rows", not the run-together "Disabled3".
              aria-label={optionName(o.label, o.count, counting)}
              checked={selected.includes(o.value)}
              onCheckedChange={(on) => toggle(o.value, on === true)}
              // Kept open, so a second answer is one more click.
              onSelect={(e) => e.preventDefault()}
            >
              {o.tone ? <StatusDot tone={o.tone} /> : null}
              <span className="min-w-0 flex-1 truncate">{o.label}</span>
              {counting === FACET_COUNTING.withheld ? null : (
                <span
                  className={cn(
                    "text-xs tabular-nums text-muted-foreground",
                    o.count === 0 && "opacity-60",
                  )}
                >
                  <Count value={o.count} />
                  {counting === FACET_COUNTING.lowerBound
                    ? AT_LEAST_MARK
                    : null}
                </span>
              )}
            </DropdownMenuCheckboxItem>
          ))
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem disabled={!active} onSelect={() => onChange([])}>
          Clear
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
