import { useEffect, useState } from "react";

/** How long a search waits after a keystroke before asking the server. */
export const SEARCH_DEBOUNCE_MS = 300;

/**
 * value once it has stopped changing for ms: a list request follows typing
 * once it settles rather than once per keystroke.
 */
export function useDebouncedValue<T>(value: T, ms = SEARCH_DEBOUNCE_MS): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setSettled(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return settled;
}
