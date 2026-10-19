"use client";

// A thin bar along the top of the window while the page waits on a first
// load or a save. Appears only once the wait outlasts LOADING_BAR_DELAY_MS,
// so a quick answer leaves no trace; still under prefers-reduced-motion.
import { useEffect, useState } from "react";
import { useIsFetching, useIsMutating } from "@tanstack/react-query";

import { LOADING_BAR_DELAY_MS, isFirstLoad } from "@/lib/loadingBar";
import { cn } from "@/lib/utils";

export function LoadingBar() {
  const loading =
    useIsFetching({ predicate: isFirstLoad }) + useIsMutating() > 0;
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!loading) {
      const off = setTimeout(() => setShown(false), 0);
      return () => clearTimeout(off);
    }
    const on = setTimeout(() => setShown(true), LOADING_BAR_DELAY_MS);
    return () => clearTimeout(on);
  }, [loading]);
  return (
    <div
      role="progressbar"
      aria-label="Loading"
      aria-hidden={!shown}
      className={cn(
        "pointer-events-none fixed inset-x-0 top-0 z-50 h-0.5 overflow-hidden transition-opacity",
        shown ? "opacity-100" : "opacity-0",
      )}
    >
      <div className="h-full w-1/3 animate-pulse bg-primary motion-reduce:animate-none" />
    </div>
  );
}
