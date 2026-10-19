"use client";

// Tells the user, in a browser notification, when one of their operations
// finishes while this tab is in the background. Opt-in on /profile; the
// browser's own permission decides too. Reads the operations the shell
// already polls (ShellContext), which keeps polling in a hidden tab while
// this is on and something runs.
import { useEffect, useRef } from "react";
import { useRouter } from "next/navigation";

import { useShell } from "@/context/ShellContext";
import { usePreference } from "@/hooks/usePreference";
import {
  finishedSince,
  noticeFor,
  statusesOf,
  type StatusByName,
} from "@/lib/operationNotices";
import { PREFERENCE_KEYS, readFlag } from "@/lib/preferences/keys";
import { SECTION } from "@/lib/navigation/sections";

/** Whether this browser can show a notification now. */
export function canNotify(): boolean {
  return (
    typeof Notification !== "undefined" && Notification.permission === "granted"
  );
}

export function OperationNotifier() {
  const { operations } = useShell();
  const [enabled] = usePreference(PREFERENCE_KEYS.notifyOperations, readFlag);
  const router = useRouter();
  const seen = useRef<StatusByName | null>(null);

  useEffect(() => {
    const ops = operations.data;
    if (!ops) return;
    const before = seen.current;
    seen.current = statusesOf(ops);
    // The first look only learns what is running.
    if (!before || !enabled || !document.hidden || !canNotify()) return;
    for (const op of finishedSince(before, ops)) {
      const { title, body } = noticeFor(op);
      const n = new Notification(title, { body, tag: op.name });
      n.onclick = () => {
        window.focus();
        router.push(SECTION.operations);
        n.close();
      };
    }
  }, [operations.data, enabled, router]);

  return null;
}
