"use client";

import { useCallback, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { JsonObject, JsonValue } from "@bufbuild/protobuf";

import type { UserSettings } from "@/gen/paladin/iam/v1/user_settings_service_pb";
import { UpdateMineRequestSchema } from "@/gen/paladin/iam/v1/user_settings_service_pb";
import { userSettingsClient } from "@/lib/connect/client";
import { fieldMask } from "@/lib/connect/fieldMask";
import type { PreferenceKey } from "@/lib/preferences/keys";
import { USER_SETTINGS_QUERY_KEY, fetchMySettings } from "@/lib/theme";

// Writes one after another: the server replaces preferences whole, so two in
// flight would each send a copy missing the other's key.
let writes: Promise<unknown> = Promise.resolve();

/**
 * One key of the user's console preferences (UserSettings.preferences): its
 * value, read through `read` so a malformed one is the default, and a setter.
 * Writes read the stored preferences first and send them back with this key
 * changed, so the other keys survive; the cache shows the change at once and
 * then what the server stored.
 */
export function usePreference<T>(
  key: PreferenceKey,
  read: (raw: unknown) => T,
) {
  const qc = useQueryClient();
  const { data } = useQuery({
    queryKey: USER_SETTINGS_QUERY_KEY,
    queryFn: ({ signal }) => fetchMySettings(signal),
    retry: false,
  });
  const value = useMemo(
    () => read(data?.preferences?.[key]),
    [data, key, read],
  );

  /**
   * Changes the key from what is stored, not from what this render saw:
   * `change` gets the stored value once the writes before it have landed,
   * and returns the new one, or undefined to leave it as it is.
   */
  const update = useCallback(
    (change: (stored: unknown) => JsonValue | undefined) => {
      const write = writes.then(async () => {
        const current = await qc.ensureQueryData({
          queryKey: USER_SETTINGS_QUERY_KEY,
          queryFn: ({ signal }) => fetchMySettings(signal),
        });
        const next = change(current?.preferences?.[key]);
        if (next === undefined) return;
        const preferences: JsonObject = {
          ...(current?.preferences ?? {}),
          [key]: next,
        };
        if (current) {
          const shown: UserSettings = { ...current, preferences };
          qc.setQueryData(USER_SETTINGS_QUERY_KEY, shown);
        }
        const saved = await userSettingsClient.updateMine({
          updateMask: fieldMask(UpdateMineRequestSchema, "preferences"),
          preferences,
        });
        qc.setQueryData(USER_SETTINGS_QUERY_KEY, saved);
      });
      // A failed write leaves the chain usable for the next one.
      writes = write.catch(() => undefined);
      return write;
    },
    [qc, key],
  );
  const set = useCallback((next: JsonValue) => update(() => next), [update]);

  return [value, set, update] as const;
}
