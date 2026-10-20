"use client";

// Ties a list's toolbar and its table to one table id, so the toolbar can
// offer the table's menu (TablePrefsMenu) and the table takes the density
// the user chose for it. Wrap the toolbar and the table of one list in it.
import { createContext, useContext } from "react";
import type React from "react";
import { QueryClientContext } from "@tanstack/react-query";

import { useTablePrefs } from "@/hooks/useTablePrefs";
import type { SortState } from "@/hooks/useTableSort";
import { DEFAULT_DENSITY, type Density } from "@/lib/preferences/tablePrefs";

type TableScopeValue = ReturnType<typeof useTablePrefs> & {
  tableId: string;
  /** The sort on screen, which the menu can make the default. */
  sort?: SortState<string>;
};

const TableScopeContext = createContext<TableScopeValue | null>(null);

/**
 * Whether a table is a page's list, with or without preferences: the one
 * table on the page the row keys move through (useTableKeyboard).
 */
const InScopeContext = createContext(false);

type TableScopeProps = {
  id: string;
  sort?: SortState<string>;
  children: React.ReactNode;
};

export function TableScope(props: TableScopeProps) {
  // The preferences are the user's settings, read through the query client;
  // where there is none (a component rendered on its own) the table keeps
  // its defaults and offers no menu.
  return (
    <InScopeContext.Provider value>
      {useContext(QueryClientContext) ? (
        <ScopeWithPrefs {...props} />
      ) : (
        props.children
      )}
    </InScopeContext.Provider>
  );
}

/** Whether the table is inside a TableScope. */
export function useInTableScope(): boolean {
  return useContext(InScopeContext);
}

function ScopeWithPrefs({ id, sort, children }: TableScopeProps) {
  const prefs = useTablePrefs(id);
  return (
    <TableScopeContext.Provider value={{ ...prefs, tableId: id, sort }}>
      {children}
    </TableScopeContext.Provider>
  );
}

export function useTableScope(): TableScopeValue | null {
  return useContext(TableScopeContext);
}

/** The density of the table in scope; the default outside one. */
export function useTableDensity(): Density {
  return useTableScope()?.prefs.density ?? DEFAULT_DENSITY;
}
