"use client";

import type { ReactNode } from "react";
import { ThemeProvider as NextThemesProvider } from "next-themes";

import { DEFAULT_THEME, PALETTES } from "@/lib/theme";

/**
 * Puts the active palette's class on <html> before first paint, from the
 * last theme this browser used, so a reload does not flash the default.
 * ThemeSync then applies the theme saved in the user's settings.
 *
 * color-scheme is left to globals.css: next-themes only writes it for
 * "light" and "dark", and would leave a stale value under "ember".
 */
export function ThemeProvider({ children }: { children: ReactNode }) {
  return (
    <NextThemesProvider
      attribute="class"
      themes={[...PALETTES]}
      defaultTheme={DEFAULT_THEME}
      enableSystem
      enableColorScheme={false}
      disableTransitionOnChange
    >
      {children}
    </NextThemesProvider>
  );
}
