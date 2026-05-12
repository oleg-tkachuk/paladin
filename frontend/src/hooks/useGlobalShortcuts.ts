"use client";

// useGlobalShortcuts — installs the platform-level keyboard
// shortcuts (g-prefix nav, Cmd+K for command palette is wired
// elsewhere). Mounted at the root layout so every page has them.
//
// g-prefix: press `g`, then within 800 ms press one of
//   d → /
//   t → /tenants
//   b → /buckets
//   s → /storage-backends
//   p → /policies
//   a → /audit
//   u → /users
//
// Inert while focus is in an editable element so typing "good
// morning" in a search field doesn't fire navigations.

import { useEffect, useRef } from "react";
import { useRouter } from "next/navigation";

const G_TARGETS: Record<string, string> = {
  d: "/",
  t: "/tenants",
  b: "/buckets",
  s: "/storage-backends",
  p: "/policies",
  a: "/audit",
  u: "/users",
};

function isEditable(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  const t = el.tagName;
  return (
    t === "INPUT" || t === "TEXTAREA" || t === "SELECT" || el.isContentEditable
  );
}

export function useGlobalShortcuts() {
  const router = useRouter();
  const gPending = useRef(false);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (isEditable(e.target)) return;
      if (e.key === "g") {
        gPending.current = true;
        setTimeout(() => {
          gPending.current = false;
        }, 800);
        return;
      }
      if (gPending.current && G_TARGETS[e.key]) {
        e.preventDefault();
        router.push(G_TARGETS[e.key]);
        gPending.current = false;
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [router]);
}
