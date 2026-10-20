"use client";

import { useEffect } from "react";

import {
  KEYBOARD_TABLE_ATTR,
  isEditable,
  rowAction,
  type RowAction,
} from "@/lib/tableKeys";

// Where a key belongs to something else: a dialog, an open menu, a listbox.
// j/k there would move the rows behind it.
const OVERLAY = "[role=dialog],[role=alertdialog],[role=menu],[role=listbox]";

/**
 * The rows the keys move through: the body's rows with cells of their own.
 * A row of one cell spanning the table is a skeleton, a failed read or an
 * empty state, not a row of the list.
 */
function bodyRows(table: HTMLElement): HTMLTableRowElement[] {
  return Array.from(
    table.querySelectorAll<HTMLTableRowElement>("tbody > tr"),
  ).filter((r) => r.cells.length > 1);
}

/** Gives a row the focus, so a screen reader reads it and Tab goes on from it. */
function focusRow(row: HTMLTableRowElement): void {
  if (!row.hasAttribute("tabindex")) row.tabIndex = -1;
  row.focus({ preventScroll: true });
  row.scrollIntoView?.({ block: "nearest" });
}

/**
 * What opening a row means: its first link, the record's page, else the row
 * itself. A row that opens its record on a click (openableRow) answers Enter
 * itself before this hears it.
 */
function openRow(row: HTMLTableRowElement): void {
  (row.querySelector<HTMLElement>("a[href]") ?? row).click();
}

/** Does `action` on `table`'s rows; false when it had nothing to do. */
function act(table: HTMLElement, action: RowAction): boolean {
  const rows = bodyRows(table);
  const active = document.activeElement;
  const current = rows.findIndex((r) => r.contains(active));
  const onRow = current >= 0 && rows[current] === active;
  switch (action) {
    case "next":
    case "previous": {
      if (rows.length === 0) return false;
      // With no row in hand, the walk starts at the first.
      const step = action === "next" ? 1 : -1;
      const to =
        current < 0
          ? 0
          : Math.min(Math.max(current + step, 0), rows.length - 1);
      focusRow(rows[to]);
      return true;
    }
    case "open":
      // Enter on a link or a button inside the row is that control's.
      if (!onRow) return false;
      openRow(rows[current]);
      return true;
    case "select": {
      const box =
        current >= 0
          ? rows[current].querySelector<HTMLElement>("[role=checkbox]")
          : null;
      if (!box) return false;
      box.click();
      return true;
    }
    case "leave":
      if (!onRow) return false;
      rows[current].blur();
      return true;
  }
}

/**
 * The keys of a list's table (lib/tableKeys): j and k, or the arrows once a
 * row has the focus, move the focus from row to row; Enter opens the row;
 * x ticks its checkbox where the table selects; Esc lets go of it. Off while
 * a field has the focus, while a dialog or a menu is open, with a modifier
 * held, and when another table on the page has the focus.
 *
 * The listener is on the window, so it hears a key after the document's
 * handlers have: a g-prefix jump ("g k") or the help's "?" claims its key
 * first, and a row that opens on Enter itself has already done so.
 */
export function useTableKeyboard(
  ref: React.RefObject<HTMLElement | null>,
  enabled: boolean,
): void {
  useEffect(() => {
    if (!enabled) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey) return;
      const table = ref.current;
      const action = rowAction(e.key);
      if (!table || !action) return;
      const target = e.target instanceof Element ? e.target : null;
      if (isEditable(target) || target?.closest(OVERLAY)) return;
      const owner = document.activeElement?.closest(`[${KEYBOARD_TABLE_ATTR}]`);
      if (owner && owner !== table) return;
      // The arrows scroll the page until a row has the focus.
      const active = document.activeElement;
      const onRow = bodyRows(table).some((r) => r === active);
      if (e.key.startsWith("Arrow") && !onRow) return;
      if (act(table, action)) e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [ref, enabled]);
}
