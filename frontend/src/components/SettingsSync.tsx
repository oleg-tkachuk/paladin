"use client";

import { useEffect, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTheme } from "next-themes";

import { setDisplayTimeZone } from "@/lib/format/locale";
import { fetchMySettings, isTheme, USER_SETTINGS_QUERY_KEY } from "@/lib/theme";

/**
 * Applies the signed-in user's settings: the theme, and the time zone times
 * are written in. Mounted inside the auth gate: there are no settings to read
 * without a session.
 *
 * The page renders once the read has settled, so the first timestamp it writes
 * is already in the user's zone rather than the browser's. A failed read keeps
 * the browser's zone and the current theme; /profile reports the error.
 *
 * The time zone applies only once the user has saved one: GetMine serves
 * defaults (UTC) to a user who never saved, and those carry no updatedAt.
 */
export function SettingsSync({ children }: { children: ReactNode }) {
  const { setTheme } = useTheme();
  const { data, isPending } = useQuery({
    queryKey: USER_SETTINGS_QUERY_KEY,
    queryFn: ({ signal }) => fetchMySettings(signal),
    retry: false,
  });
  const theme = data?.theme;
  const timeZone = data?.updatedAt ? data.timezone : undefined;

  useEffect(() => {
    if (theme && isTheme(theme)) setTheme(theme);
  }, [theme, setTheme]);

  // During render, not in an effect: the children below format with it in
  // this same pass.
  setDisplayTimeZone(timeZone);

  return isPending ? null : children;
}
