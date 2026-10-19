"use client";

import { useEffect, useRef } from "react";

/**
 * Runs `fn` immediately, then every `intervalMs` — but only while the
 * tab is visible. Hidden tabs skip ticks (no RPC loops piling up in
 * background tabs); returning to the tab fires one catch-up tick right
 * away so the data is fresh before the next scheduled poll.
 *
 * `fn` is kept in a ref so callers don't need a stable callback; the
 * interval only re-arms when `intervalMs` changes. Not `enabled` — a view
 * whose Live switch is off — it does nothing at all.
 */
export function useVisiblePolling(
  fn: () => void,
  intervalMs: number,
  enabled = true,
  /** Keep polling in a hidden tab too, for news the user asked to hear. */
  whileHidden = false,
) {
  const fnRef = useRef(fn);
  // Ref updated in an effect, not during render (react-hooks/refs):
  // ticks only ever fire after the commit anyway.
  useEffect(() => {
    fnRef.current = fn;
  });

  useEffect(() => {
    if (!enabled) return;
    // `alive` guards every invocation so a tick that was already queued
    // (interval or visibilitychange) can't call fn after unmount — e.g.
    // a fast route transition between the effect committing and a poll
    // firing. Without it the consumer's fetch resolves into setState on
    // a dead component (wasted work + a brief flash on re-mount).
    let alive = true;
    const run = () => {
      if (alive && (whileHidden || !document.hidden)) fnRef.current();
    };
    run(); // initial fetch (skipped if the tab is already hidden)
    const id = setInterval(run, intervalMs);
    document.addEventListener("visibilitychange", run);
    return () => {
      alive = false;
      clearInterval(id);
      document.removeEventListener("visibilitychange", run);
    };
  }, [intervalMs, enabled, whileHidden]);
}
