import { ConnectError, Code } from "@connectrpc/connect";

import { userSettingsClient } from "@/lib/connect/client";

// The themes a user can save. The same set is apiutil.Themes on the server
// and the user_settings theme CHECK in the database; themes.test.ts keeps
// this list equal to the CHECK.
export const THEME_SYSTEM = "system";
export const THEME_LIGHT = "light";
export const THEME_DARK = "dark";
export const THEME_EMBER = "ember";

export type Theme =
  | typeof THEME_SYSTEM
  | typeof THEME_LIGHT
  | typeof THEME_DARK
  | typeof THEME_EMBER;

// The palettes: each is a class on <html> that globals.css styles. "system"
// is not one — it resolves to light or dark from the browser's preference.
export const PALETTES: readonly Theme[] = [
  THEME_LIGHT,
  THEME_DARK,
  THEME_EMBER,
];

// What the console shows before the user's setting has loaded, and on /login.
export const DEFAULT_THEME: Theme = THEME_DARK;

export const THEME_OPTIONS: readonly { value: Theme; label: string }[] = [
  { value: THEME_SYSTEM, label: "Match system" },
  { value: THEME_LIGHT, label: "Light" },
  { value: THEME_DARK, label: "Dark" },
  { value: THEME_EMBER, label: "Ember" },
];

export function isTheme(value: string): value is Theme {
  return THEME_OPTIONS.some((o) => o.value === value);
}

// One cache entry for the caller's settings, shared by /profile and the
// shell that applies the theme, so a save on /profile reaches both.
export const USER_SETTINGS_QUERY_KEY = ["userSettings"] as const;

/**
 * The caller's settings, or null when they have never saved any: the backend
 * creates the row on the first UpdateMine, so NotFound is a normal state.
 * Decided on the code, not by matching the message text.
 */
export async function fetchMySettings(signal?: AbortSignal) {
  try {
    return await userSettingsClient.getMine({}, { signal });
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.NotFound) {
      return null;
    }
    throw err;
  }
}
