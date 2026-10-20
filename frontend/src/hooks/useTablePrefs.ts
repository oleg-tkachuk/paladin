"use client";

import { useCallback, useMemo } from "react";

import { usePreference } from "@/hooks/usePreference";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";
import {
  readTablePrefs,
  toggleHidden,
  withTablePrefs,
  type DefaultSort,
  type Density,
  type TablePrefs,
} from "@/lib/preferences/tablePrefs";
import type { PageSize } from "@/lib/table/paging";
import { DEFAULT_VIEW_MODE, type ViewMode } from "@/lib/table/viewMode";

const asStored = (raw: unknown) => raw;

/**
 * One table's preferences for this user (console.tables[tableId]) and the
 * changes the table menu makes to them, each written against what is stored.
 */
export function useTablePrefs(tableId: string) {
  const [stored, , update] = usePreference(PREFERENCE_KEYS.tables, asStored);
  const prefs = useMemo(
    () => readTablePrefs(stored, tableId),
    [stored, tableId],
  );

  const change = useCallback(
    (fn: (p: TablePrefs) => TablePrefs) =>
      update((raw) => withTablePrefs(raw, tableId, fn) as never),
    [update, tableId],
  );

  return {
    prefs,
    setDensity: (density: Density) => change((p) => ({ ...p, density })),
    toggleColumn: (column: string) => change((p) => toggleHidden(p, column)),
    saveSort: (sort: DefaultSort) => change((p) => ({ ...p, sort })),
    clearSort: () => change((p) => ({ ...p, sort: undefined })),
    setPageSize: (pageSize: PageSize) => change((p) => ({ ...p, pageSize })),
    // The default view is left out, as an unset one.
    saveView: (view: ViewMode) =>
      change((p) => ({
        ...p,
        view: view === DEFAULT_VIEW_MODE ? undefined : view,
      })),
  };
}
