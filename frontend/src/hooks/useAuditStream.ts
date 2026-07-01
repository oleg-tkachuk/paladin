"use client";

import { useEffect, useRef, useState } from "react";

/**
 * useAuditStream subscribes to the live audit feed (/api/audit/stream, an
 * SSE proxy to the admin plane) and invokes onEvent for every new audit
 * entry pushed for the caller's tenant.
 *
 * The event payload is a compact projection (no before/after diffs), so the
 * intended use is as an INVALIDATION SIGNAL: the /audit page debounce-calls
 * its existing refresh() and the full rows flow through ListAuditLog — one
 * code path for data, SSE only says "there's something new".
 *
 * EventSource auto-reconnects on drop; `connected` tracks the socket state
 * so the UI can render a live/offline badge. Disabled → no socket at all.
 */
export function useAuditStream(enabled: boolean, onEvent: () => void) {
  const [connected, setConnected] = useState(false);
  // Keep the latest callback in a ref so a re-render doesn't tear the socket
  // down and reconnect (the socket effect below deliberately omits onEvent).
  // Updated from an effect, not during render (react-hooks/refs).
  const onEventRef = useRef(onEvent);
  useEffect(() => {
    onEventRef.current = onEvent;
  }, [onEvent]);

  useEffect(() => {
    if (!enabled) return;
    const es = new EventSource("/api/audit/stream");
    // Socket events are the only writers of `connected` — the disabled state
    // is derived below instead of set from the effect body (lint: no
    // synchronous setState inside effects).
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false); // EventSource retries by itself
    es.addEventListener("audit", () => {
      onEventRef.current();
    });
    return () => {
      es.close();
    };
  }, [enabled]);

  // Derived: a disabled stream is never "connected"; a just-re-enabled one
  // may show the previous socket's state for the instant before onopen/onerror
  // fires — harmless for a status badge.
  return { connected: enabled && connected };
}
