"use client";

import { useMemo, useState } from "react";

import { usePreferenceSnapshot } from "@/hooks/usePreference";
import { useUrlState } from "@/hooks/useUrlState";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";
import { readTablePrefs } from "@/lib/preferences/tablePrefs";
import {
  DEFAULT_VIEW_MODE,
  viewModeCodec,
  type ViewMode,
} from "@/lib/table/viewMode";

/**
 * A list's view — table, tiles or summary — kept in the URL. A URL that
 * names none opens the view the user saved for the table
 * (console.tables[tableId].view), read once as a table's default sort is;
 * ViewModeSwitcher saves the choice through the table's scope.
 */
export function useTableViewMode(tableId: string) {
  const stored = usePreferenceSnapshot(PREFERENCE_KEYS.tables);
  const [fallback] = useState<ViewMode>(
    () => readTablePrefs(stored, tableId).view ?? DEFAULT_VIEW_MODE,
  );
  const codec = useMemo(() => viewModeCodec(fallback), [fallback]);
  const [view, setView] = useUrlState(codec, fallback);
  return { view, setView };
}
