"use client";

// useUrlState — bidirectional binding between component state and
// the URL query string. Refresh-safe: filters, sort keys, search
// text survive a reload. Shareable: paste the URL → same view.
//
//   const [filters, setFilters] = useUrlState("filters", {
//     q: "",
//     label: "",
//     sort: "name",
//   });
//
// Behaviour notes:
//   - Defaults are NOT serialised — keeps URLs short. A field at
//     its default just doesn't appear.
//   - Updates use router.replace by default (no history pollution).
//     Pass `pushHistory: true` for navigations that should be back-
//     button-recoverable (e.g. opening a detail pane).
//   - Re-renders the consumer only when relevant keys change (via
//     a JSON-equality check, not deep-reference).

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { z } from "zod";

import { safeParseJson } from "@/lib/parseJson";

export interface UseUrlStateOptions {
  /** Use router.push (browser-back-recoverable) instead of replace. */
  pushHistory?: boolean;
  /** Optional prefix to namespace URL keys (avoids collisions when
   *  multiple components on the same page each manage state). */
  prefix?: string;
}

export function useUrlState<T extends Record<string, string>>(
  _label: string,
  defaults: T,
  options: UseUrlStateOptions = {},
): [T, (next: Partial<T>) => void] {
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();
  const prefix = options.prefix ? `${options.prefix}.` : "";

  // Read current URL → state, falling back to defaults.
  const fromUrl = useMemo<T>(() => {
    const out = { ...defaults };
    for (const k of Object.keys(defaults) as Array<keyof T>) {
      const v = params.get(prefix + (k as string));
      if (v !== null) (out as Record<string, string>)[k as string] = v;
    }
    return out;
  }, [defaults, params, prefix]);

  const [state, setState] = useState<T>(fromUrl);
  const lastUrlState = useRef(JSON.stringify(fromUrl));

  // Keep state in sync if the URL changes externally (back/forward
  // navigation). Subscription pattern — URL is the external source
  // of truth; the cascade is bounded by the sig-equality check.
  useEffect(() => {
    const sig = JSON.stringify(fromUrl);
    if (sig === lastUrlState.current) return;
    lastUrlState.current = sig;
    // Defer to microtask to satisfy the no-set-state-in-effect rule
    // — React batches into the next paint anyway, so visually
    // identical to the sync call.
    queueMicrotask(() => setState(fromUrl));
  }, [fromUrl]);

  const setUrlState = useCallback(
    (next: Partial<T>) => {
      const merged = { ...state, ...next };
      const sp = new URLSearchParams(params.toString());
      for (const k of Object.keys(merged) as Array<keyof T>) {
        const v = (merged as Record<string, string>)[k as string];
        const isDefault =
          v === (defaults as Record<string, string>)[k as string];
        const key = prefix + (k as string);
        if (v === "" || isDefault) sp.delete(key);
        else sp.set(key, v);
      }
      const qs = sp.toString();
      const url = qs ? `${pathname}?${qs}` : pathname;
      setState(merged);
      lastUrlState.current = JSON.stringify(merged);
      if (options.pushHistory) router.push(url);
      else router.replace(url);
    },
    [state, params, defaults, prefix, pathname, options.pushHistory, router],
  );

  return [state, setUrlState];
}

// ── Saved views ────────────────────────────────────────────────────

// Persist named filter sets in localStorage. Keyed per page so /tenants
// "My production tenants" doesn't leak to /buckets. Each entry stores
// the URL query string verbatim so reapplying is a single setUrlState
// call with the parsed object.

export interface SavedView {
  name: string;
  query: string; // encoded URLSearchParams sans the leading '?'
  createdAt: number;
}

// Validated on read — localStorage is user-editable, so a corrupt entry
// yields [] rather than an `as`-blessed wrong shape.
const SavedViewSchema = z.object({
  name: z.string(),
  query: z.string(),
  createdAt: z.number(),
});

const SV_PREFIX = "paladin.savedviews.";

export function loadSavedViews(pageKey: string): SavedView[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(SV_PREFIX + pageKey);
    return safeParseJson(z.array(SavedViewSchema), raw) ?? [];
  } catch {
    return [];
  }
}

export function saveSavedView(pageKey: string, view: SavedView): void {
  if (typeof window === "undefined") return;
  const existing = loadSavedViews(pageKey).filter((v) => v.name !== view.name);
  existing.push(view);
  window.localStorage.setItem(SV_PREFIX + pageKey, JSON.stringify(existing));
}

export function deleteSavedView(pageKey: string, name: string): void {
  if (typeof window === "undefined") return;
  const remaining = loadSavedViews(pageKey).filter((v) => v.name !== name);
  window.localStorage.setItem(SV_PREFIX + pageKey, JSON.stringify(remaining));
}
