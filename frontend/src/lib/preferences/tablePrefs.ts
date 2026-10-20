// How each table looks to this user: its density, the columns they hid, the
// sort it opens with and the view it is laid out in. One entry per table id in console.tables, read
// through a schema so a malformed one is the default, never a broken table.
import { z } from "zod";

import { VIEW_MODES } from "@/lib/table/viewMode";

/** How tightly rows are set. */
export const DENSITIES = ["comfortable", "compact"] as const;
export type Density = (typeof DENSITIES)[number];
export const DEFAULT_DENSITY: Density = "comfortable";

const SortSchema = z.object({
  column: z.string().min(1),
  direction: z.enum(["asc", "desc"]),
});

const TablePrefsSchema = z.object({
  density: z.enum(DENSITIES).optional(),
  hidden: z.array(z.string()).optional(),
  sort: SortSchema.optional(),
  /** Table, tiles or summary (lib/table/viewMode). */
  view: z.enum(VIEW_MODES).optional(),
});

export type TablePrefs = z.infer<typeof TablePrefsSchema>;
export type DefaultSort = z.infer<typeof SortSchema>;

const AllTablesSchema = z.record(z.string(), z.unknown());

/** One table's preferences out of the stored map; a bad entry is none. */
export function readTablePrefs(stored: unknown, tableId: string): TablePrefs {
  const all = AllTablesSchema.safeParse(stored);
  if (!all.success) return {};
  const one = TablePrefsSchema.safeParse(all.data[tableId]);
  return one.success ? one.data : {};
}

/**
 * The stored map with one table's preferences changed. The other tables'
 * entries are kept as stored, even ones this console cannot read.
 */
export function withTablePrefs(
  stored: unknown,
  tableId: string,
  change: (current: TablePrefs) => TablePrefs,
): Record<string, unknown> {
  const all = AllTablesSchema.safeParse(stored);
  const map = all.success ? { ...all.data } : {};
  const next = change(readTablePrefs(map, tableId));
  const pruned = Object.fromEntries(
    Object.entries(next).filter(([, v]) => v !== undefined),
  );
  if (Object.keys(pruned).length === 0) delete map[tableId];
  else map[tableId] = pruned;
  return map;
}

/** A column hidden, or shown again. */
export function toggleHidden(prefs: TablePrefs, column: string): TablePrefs {
  const hidden = new Set(prefs.hidden ?? []);
  if (hidden.has(column)) hidden.delete(column);
  else hidden.add(column);
  return { ...prefs, hidden: hidden.size ? [...hidden].sort() : undefined };
}

/** The table's default sort, when it names a column the table sorts by. */
export function defaultSortFor<C extends string>(
  prefs: TablePrefs,
  columns: readonly C[],
): { column: C; direction: "asc" | "desc" } | null {
  const column = columns.find((c) => c === prefs.sort?.column);
  return column && prefs.sort
    ? { column, direction: prefs.sort.direction }
    : null;
}
