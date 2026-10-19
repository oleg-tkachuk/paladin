"use client";

import { useCallback } from "react";

import { PREFERENCE_KEYS, type ResourceLink } from "@/lib/preferences/keys";
import {
  readLinks,
  sameLinks,
  togglePinned,
  withVisit,
} from "@/lib/preferences/resourceLinks";
import { usePreference } from "@/hooks/usePreference";

/**
 * The resources this user opened lately and pinned, kept in their settings
 * so they follow them to another browser. A visit that changes nothing
 * writes nothing.
 */
export function useResourceLinks() {
  const [recent, , updateRecent] = usePreference(
    PREFERENCE_KEYS.recent,
    readLinks,
  );
  const [pinned, , updatePinned] = usePreference(
    PREFERENCE_KEYS.pinned,
    readLinks,
  );

  // Both change what is stored, not this render's copy: a visit made before
  // the settings loaded would otherwise replace the history with itself.
  const visit = useCallback(
    (link: ResourceLink) => {
      void updateRecent((stored) => {
        const before = readLinks(stored);
        const next = withVisit(before, link);
        return sameLinks(next, before) ? undefined : next;
      }).catch(() => undefined);
    },
    [updateRecent],
  );
  const togglePin = useCallback(
    (link: ResourceLink) =>
      updatePinned((stored) => togglePinned(readLinks(stored), link)),
    [updatePinned],
  );
  const isPinned = useCallback(
    (href: string) => pinned.some((p) => p.href === href),
    [pinned],
  );

  return { recent, pinned, visit, togglePin, isPinned };
}
