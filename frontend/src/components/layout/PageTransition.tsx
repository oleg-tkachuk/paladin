"use client";

// The page area's view transition: a navigation fades the page out and the
// next one in (globals.css times it), where the browser runs view
// transitions and the user has not asked for less motion. The boundary is
// always rendered and only its class changes, so turning motion on or off —
// as hydration does, the server knowing neither — never remounts the page.
import { useSyncExternalStore, ViewTransition } from "react";

import {
  PAGE_TRANSITION_CLASS,
  pageTransitionsOn,
  REDUCED_MOTION_QUERY,
} from "@/lib/ui/motion";

/** The view-transition class that runs no animation. */
const NO_TRANSITION = "none";

function subscribe(onChange: () => void): () => void {
  const query = window.matchMedia?.(REDUCED_MOTION_QUERY);
  query?.addEventListener("change", onChange);
  return () => query?.removeEventListener("change", onChange);
}

const onClient = () => pageTransitionsOn(document, window);
const onServer = () => false;

export function PageTransition({ children }: { children: React.ReactNode }) {
  const on = useSyncExternalStore(subscribe, onClient, onServer);
  return (
    <ViewTransition default={on ? PAGE_TRANSITION_CLASS : NO_TRANSITION}>
      {children}
    </ViewTransition>
  );
}
