"use client";

import { useEffect, useRef } from "react";

/**
 * Runs `fn` immediately, then every `intervalMs` — but only while the
 * tab is visible. Hidden tabs skip ticks (no RPC loops piling up in
 * background tabs); returning to the tab fires one catch-up tick right
 * away so the data is fresh before the next scheduled poll.
 *
 * `fn` is kept in a ref so callers don't need a stable callback; the
 * interval only re-arms when `intervalMs` changes.
 */
export function useVisiblePolling(fn: () => void, intervalMs: number) {
  const fnRef = useRef(fn);
  // Ref updated in an effect, not during render (react-hooks/refs):
  // ticks only ever fire after the commit anyway.
  useEffect(() => {
    fnRef.current = fn;
  });

  useEffect(() => {
    fnRef.current();
    const tick = () => {
      if (!document.hidden) fnRef.current();
    };
    const id = setInterval(tick, intervalMs);
    const onVisibilityChange = () => {
      if (!document.hidden) fnRef.current();
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [intervalMs]);
}
