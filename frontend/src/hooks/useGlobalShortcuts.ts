"use client";

// useGlobalShortcuts — the one handler for the g-prefix jumps, and for
// `/`, which focuses the page's list search. The table it serves is
// NAV_JUMPS in lib/navShortcuts; the command palette and the keyboard help
// label their entries from the same table.
//
// Mounted inside the scope provider: b, k and t go to the scoped
// tenant's pages while one is in scope. Inert while focus is in an
// editable element so typing "good morning" in a search field doesn't
// fire navigations, and with Cmd/Ctrl/Alt held so browser and palette
// shortcuts pass through.

import { useEffect, useRef } from "react";
import { useRouter } from "next/navigation";

import { useActingTenant } from "@/hooks/useActingTenant";
import {
  NAV_CHORD_WINDOW_MS,
  NAV_PREFIX,
  jumpTarget,
  tenantBasePath,
} from "@/lib/navShortcuts";
import { SEARCH_KEY, TABLE_SEARCH_ATTR, isEditable } from "@/lib/tableKeys";

export function useGlobalShortcuts() {
  const router = useRouter();
  // The tenant on screen, as the command palette names it (ADR-0033).
  const tenantBase = tenantBasePath(useActingTenant());
  // When the prefix was pressed; null while no chord is pending.
  const armedAt = useRef<number | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (isEditable(e.target)) return;
      const pending =
        armedAt.current !== null &&
        Date.now() - armedAt.current < NAV_CHORD_WINDOW_MS;
      armedAt.current = null;
      const target = pending ? jumpTarget(e.key, tenantBase) : null;
      if (target !== null) {
        e.preventDefault();
        router.push(target);
        return;
      }
      if (e.key === SEARCH_KEY) {
        // The page's list search, the first one where a page has several:
        // the main list's toolbar comes before any card's.
        const search = document.querySelector<HTMLElement>(
          `[${TABLE_SEARCH_ATTR}]`,
        );
        if (search) {
          e.preventDefault();
          search.focus();
        }
        return;
      }
      if (e.key === NAV_PREFIX) armedAt.current = Date.now();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [router, tenantBase]);
}
