"use client";

import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTheme } from "next-themes";

import { fetchMySettings, isTheme, USER_SETTINGS_QUERY_KEY } from "@/lib/theme";

/**
 * Applies the theme saved in the signed-in user's settings. Mounted inside
 * the auth gate: there are no settings to read without a session. A failed
 * read keeps the current theme; /profile reports the error.
 */
export function ThemeSync() {
  const { setTheme } = useTheme();
  const { data } = useQuery({
    queryKey: USER_SETTINGS_QUERY_KEY,
    queryFn: ({ signal }) => fetchMySettings(signal),
    retry: false,
  });
  const saved = data?.theme;

  useEffect(() => {
    if (saved && isTheme(saved)) setTheme(saved);
  }, [saved, setTheme]);

  return null;
}
