// How this user reads a diff, wherever the console shows one: only the
// changes or the whole text, and in one column or two. Kept in
// console.diff and read through a schema, so a value another client wrote
// reads as the default rather than breaking the view.
import { z } from "zod";

export interface DiffPrefs {
  /** Only the changed lines and the lines around them. */
  compact: boolean;
  /** The old text and the new side by side, rather than in one column. */
  split: boolean;
}

// Compact and in one column, as Argo CD opens its diff: most edits touch a few
// lines of a long text, and one column fits a narrow panel.
const DEFAULT_DIFF_PREFS: DiffPrefs = { compact: true, split: false };

const DiffPrefsSchema = z.object({
  compact: z.boolean().optional(),
  split: z.boolean().optional(),
});

/** The stored preference, each part the default when it is missing or bad. */
export function readDiffPrefs(raw: unknown): DiffPrefs {
  const parsed = DiffPrefsSchema.safeParse(raw);
  if (!parsed.success) return DEFAULT_DIFF_PREFS;
  return { ...DEFAULT_DIFF_PREFS, ...stripUndefined(parsed.data) };
}

function stripUndefined(prefs: Partial<DiffPrefs>): Partial<DiffPrefs> {
  return Object.fromEntries(
    Object.entries(prefs).filter(([, v]) => v !== undefined),
  );
}
