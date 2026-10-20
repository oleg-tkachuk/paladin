"use client";

import { NoticeBanner } from "@/components/ui/Notice";
import { useShell } from "@/context/ShellContext";
import { bannerAt } from "@/lib/banner";

/**
 * The operator's banner at one edge of the page, when it is set to stand
 * there. A strip the width of the content column, in the page's flow rather
 * than fixed: at the top it sits under the sticky top bar and scrolls away
 * beneath it, so it never covers the bar or a list's sticky toolbar.
 */
export function PlatformBanner({ edge }: { edge: "top" | "bottom" }) {
  const { banner } = useShell();
  if (!banner || !bannerAt(banner, edge)) return null;
  return (
    <NoticeBanner
      notice={banner}
      className={
        edge === "top" ? "border-x-0 border-t-0" : "border-x-0 border-b-0"
      }
    />
  );
}
