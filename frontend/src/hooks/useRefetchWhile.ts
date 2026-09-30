"use client";

import { useEffect, useRef } from "react";

/**
 * Calls `fn` every `intervalMs` for as long as `active` holds and the tab is
 * visible — for a list showing rows the server has yet to settle, which
 * otherwise sit in their transient state until someone reloads.
 */
export function useRefetchWhile(
  active: boolean,
  fn: () => void,
  intervalMs: number,
) {
  const fnRef = useRef(fn);
  useEffect(() => {
    fnRef.current = fn;
  });

  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => {
      if (!document.hidden) fnRef.current();
    }, intervalMs);
    return () => clearInterval(id);
  }, [active, intervalMs]);
}
