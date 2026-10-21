"use client";

import * as React from "react";

import { cn } from "@/lib/utils";
import { labelCells } from "@/lib/table/cellLabels";
import { useInTableScope, useTableDensity } from "@/components/ui/TableScope";
import { useTableKeyboard } from "@/hooks/useTableKeyboard";
import { KEYBOARD_TABLE_ATTR } from "@/lib/tableKeys";
import type { Density } from "@/lib/preferences/tablePrefs";
import { HOVER_SURFACE, ROW_FOCUS_RING } from "@/lib/ui/states";

/** How rows are set, by the density the user chose for the table. */
const DENSITY_CLASS: Record<Density, string> = {
  comfortable: "",
  compact:
    "[&_[data-slot=table-cell]]:py-1 [&_[data-slot=table-head]]:h-8 text-xs",
};

/**
 * Below a phone's width of table (the container's @md), a row is set out as
 * a card: the header is hidden and each cell reads "column: value", from the
 * data-label labelCells gives it. A cell the page hides at that width stays
 * hidden, and a row spanning the table (an empty state) stays one block.
 */
/**
 * For a cell the page hides on narrow tables (`hidden @3xl:table-cell`) that
 * still belongs on a phone's card, where the row has room for it again.
 */
export const SHOWN_IN_CARD = "@max-md:flex";

const STACKED_CLASS = [
  "@max-md:[&_thead]:hidden",
  "@max-md:[&_tbody]:block @max-md:[&_tr]:block",
  "@max-md:[&_tbody_tr]:mb-2 @max-md:[&_tbody_tr]:rounded-lg @max-md:[&_tbody_tr]:border",
  "@max-md:[&_td:not(.hidden)]:flex @max-md:[&_td]:items-center @max-md:[&_td]:justify-between @max-md:[&_td]:gap-3 @max-md:[&_td]:whitespace-normal",
  "@max-md:[&_td[data-label]]:before:content-[attr(data-label)] @max-md:[&_td]:before:text-xs @max-md:[&_td]:before:text-muted-foreground",
].join(" ");

function Table({
  className,
  keyboardRows,
  ...props
}: React.ComponentProps<"table"> & {
  /**
   * Whether j/k, Enter, x and Esc work its rows (useTableKeyboard). A page's
   * list, the table in its TableScope, does by default; a table in a card
   * beside it does not, so one key moves one list.
   */
  keyboardRows?: boolean;
}) {
  // Inside a TableScope, the user's density for that table.
  const density = useTableDensity();
  const inScope = useInTableScope();
  const navigable = keyboardRows ?? inScope;
  // Each body cell learns its column's name, and learns it again as rows
  // come and go, for the stacked layout above.
  const ref = React.useRef<HTMLTableElement>(null);
  React.useEffect(() => {
    const table = ref.current;
    if (!table) return;
    labelCells(table);
    const observer = new MutationObserver(() => labelCells(table));
    observer.observe(table, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, []);
  useTableKeyboard(ref, navigable);
  return (
    // A size container, so a page shows optional columns by the width the
    // table actually has (`hidden @2xl:table-cell`), not the viewport's: the
    // sidebar takes a fixed share of the window, and a viewport breakpoint
    // counts width the table never gets.
    <div
      data-slot="table-container"
      className="@container relative w-full overflow-x-auto"
    >
      <table
        ref={ref}
        data-slot="table"
        data-density={density}
        {...(navigable ? { [KEYBOARD_TABLE_ATTR]: true } : {})}
        className={cn(
          "w-full caption-bottom text-sm",
          DENSITY_CLASS[density],
          STACKED_CLASS,
          className,
        )}
        {...props}
      />
    </div>
  );
}

function TableHeader({ className, ...props }: React.ComponentProps<"thead">) {
  return (
    <thead
      data-slot="table-header"
      // On the band a card's header sits on, so a table's head reads as
      // the same strip.
      className={cn("bg-band [&_tr]:border-b", className)}
      {...props}
    />
  );
}

function TableBody({ className, ...props }: React.ComponentProps<"tbody">) {
  return (
    <tbody
      data-slot="table-body"
      className={cn("[&_tr:last-child]:border-0", className)}
      {...props}
    />
  );
}

function TableFooter({ className, ...props }: React.ComponentProps<"tfoot">) {
  return (
    <tfoot
      data-slot="table-footer"
      className={cn(
        "border-t bg-muted/50 font-medium [&>tr]:last:border-b-0",
        className,
      )}
      {...props}
    />
  );
}

/**
 * Marks the row a link points at (useLinkedRow). The class below spells the
 * attribute out, since Tailwind finds a class only as literal text; a test
 * holds the two together.
 */
export const LINKED_ROW_ATTR = "data-linked";

function TableRow({ className, ...props }: React.ComponentProps<"tr">) {
  return (
    <tr
      data-slot="table-row"
      className={cn(
        "border-b transition-colors has-aria-expanded:bg-muted/50 data-[state=selected]:bg-muted data-linked:bg-primary/10 data-linked:shadow-[inset_2px_0_0_var(--primary)]",
        HOVER_SURFACE,
        ROW_FOCUS_RING,
        className,
      )}
      {...props}
    />
  );
}

/**
 * A column of figures: right-aligned, in tabular figures, so units, tens and
 * thousands line up row under row. Its head takes the same, to sit over them.
 */
const NUMERIC = "text-right tabular-nums";

/** Marks a cell or head of a column of figures. */
export const NUMERIC_ATTR = "data-numeric";

type Numeric = { numeric?: boolean };

function TableHead({
  className,
  numeric,
  ...props
}: React.ComponentProps<"th"> & Numeric) {
  return (
    <th
      data-slot="table-head"
      {...(numeric ? { [NUMERIC_ATTR]: true } : {})}
      className={cn(
        "h-10 px-2 text-left align-middle font-medium whitespace-nowrap text-foreground [&:has([role=checkbox])]:pr-0",
        numeric && NUMERIC,
        className,
      )}
      {...props}
    />
  );
}

function TableCell({
  className,
  numeric,
  ...props
}: React.ComponentProps<"td"> & Numeric) {
  return (
    <td
      data-slot="table-cell"
      {...(numeric ? { [NUMERIC_ATTR]: true } : {})}
      className={cn(
        "p-2 align-middle whitespace-nowrap [&:has([role=checkbox])]:pr-0",
        numeric && NUMERIC,
        className,
      )}
      {...props}
    />
  );
}

function TableCaption({
  className,
  ...props
}: React.ComponentProps<"caption">) {
  return (
    <caption
      data-slot="table-caption"
      className={cn("mt-4 text-sm text-muted-foreground", className)}
      {...props}
    />
  );
}

export {
  Table,
  TableHeader,
  TableBody,
  TableFooter,
  TableHead,
  TableRow,
  TableCell,
  TableCaption,
};
