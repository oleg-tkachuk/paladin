"use client";

import { useState } from "react";

import { usePreference } from "@/hooks/usePreference";
import {
  noticeKey,
  readDismissed,
  withDismissed,
  type Notice,
} from "@/lib/notice";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";

/**
 * Whether the reader has dismissed this notice, and how they do. The
 * dismissal is kept in their settings under what the notice says
 * (noticeKey), so it follows them to another browser and a changed notice
 * shows again. A permanent notice is never dismissed.
 */
export function useNoticeDismissal(notice: Notice | null) {
  const [stored, , update] = usePreference(
    PREFERENCE_KEYS.dismissedNotices,
    readDismissed,
  );
  const key = notice ? noticeKey(notice) : null;
  // Dismissed in this tab: the notice goes at once, and stays gone for the
  // visit even when the settings cannot be saved.
  const [here, setHere] = useState<string | null>(null);
  const dismissed =
    key !== null &&
    !notice?.permanent &&
    (here === key || stored.includes(key));

  const dismiss = () => {
    if (key === null) return;
    setHere(key);
    // A failed save costs the dismissal on the next visit, nothing more.
    update((raw) => withDismissed(raw, key)).catch(() => undefined);
  };

  return { dismissed, dismiss };
}
