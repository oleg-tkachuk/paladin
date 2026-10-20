// The keys a list answers: `/` to its search, and j/k through its rows, after
// Gmail's and GitHub's. One table serves the handlers (useTableKeyboard,
// useGlobalShortcuts) and the keyboard help, so the help cannot list a key
// nothing answers.

/** Focuses the list's search box, from anywhere on the page. */
export const SEARCH_KEY = "/";

/** Marks the search box SEARCH_KEY focuses: the list's, in its TableToolbar. */
export const TABLE_SEARCH_ATTR = "data-table-search";

/** Marks a table whose rows the row keys move through. */
export const KEYBOARD_TABLE_ATTR = "data-keyboard-rows";

/** What each row key does, by the KeyboardEvent.key values that do it. */
export const ROW_KEYS = {
  next: ["j", "ArrowDown"],
  previous: ["k", "ArrowUp"],
  open: ["Enter"],
  select: ["x"],
  leave: ["Escape"],
} as const satisfies Record<string, readonly string[]>;

export type RowAction = keyof typeof ROW_KEYS;

/** The row action a key does; null for a key the rows do not answer. */
export function rowAction(key: string): RowAction | null {
  const found = (Object.keys(ROW_KEYS) as RowAction[]).find((a) =>
    (ROW_KEYS[a] as readonly string[]).includes(key),
  );
  return found ?? null;
}

/** How the help writes a key that is not a character. */
const KEY_GLYPH: Record<string, string> = {
  ArrowDown: "↓",
  ArrowUp: "↑",
  Escape: "Esc",
};

const ROW_HELP: Record<RowAction, string> = {
  next: "Next row",
  previous: "Previous row",
  open: "Open the row",
  select: "Select the row, where the list selects",
  leave: "Leave the rows",
};

/** The list keys as the keyboard help shows them: each the keys that do it. */
export const TABLE_SHORTCUTS: readonly { keys: string[]; desc: string }[] = [
  { keys: [SEARCH_KEY], desc: "Search the list" },
  ...(Object.keys(ROW_KEYS) as RowAction[]).map((a) => ({
    keys: ROW_KEYS[a].map((k) => KEY_GLYPH[k] ?? k),
    desc: ROW_HELP[a],
  })),
];

/**
 * Whether a key press is typing: in a field, a select or anything editable,
 * where a letter is a letter and not a shortcut.
 */
export function isEditable(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  const t = el.tagName;
  return (
    t === "INPUT" || t === "TEXTAREA" || t === "SELECT" || el.isContentEditable
  );
}
