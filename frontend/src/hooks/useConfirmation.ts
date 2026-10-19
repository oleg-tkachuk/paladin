"use client";

import { useCallback, useEffect, useState } from "react";

/** How long a confirmation stays beside what it confirms. */
export const CONFIRMATION_MS = 2_000;

/**
 * A success confirmed where it happened — beside the button, in the row —
 * rather than in a toast across the screen. `confirm` shows it; it goes by
 * itself after `ms`.
 */
export function useConfirmation(ms: number = CONFIRMATION_MS) {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!shown) return;
    const t = setTimeout(() => setShown(false), ms);
    return () => clearTimeout(t);
  }, [shown, ms]);
  const confirm = useCallback(() => setShown(true), []);
  return { shown, confirm };
}
