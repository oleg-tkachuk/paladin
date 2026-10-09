"use client";

import { useCallback, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ClockIcon,
  GlobeAltIcon,
  PaintBrushIcon,
  UserCircleIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { useNotification } from "@/components/ui/Notification";
import { ChangePasswordCard } from "@/components/features/ChangePasswordCard";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { useAuth } from "@/context/AuthContext";
import { userSettingsClient } from "@/lib/connect/client";
import { isAbortError, errorMessage } from "@/hooks/errorContract";
import { Timestamp } from "@/components/Timestamp";
import { SETTINGS_APPLIED_NOTE } from "./_constants";
import {
  fetchMySettings,
  THEME_OPTIONS,
  THEME_SYSTEM,
  USER_SETTINGS_QUERY_KEY,
} from "@/lib/theme";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { fieldMask } from "@/lib/connect/fieldMask";
import { UpdateMineRequestSchema } from "@/gen/paladin/iam/v1/user_settings_service_pb";

// /profile — self-service editor backed by iam/v1.UserSettingsService.
// Tenant comes from the JWT, so the page always operates on the calling
// principal's row (GetMine / UpdateMine) — no Cedar round-trip needed.
//
// Only the three first-class columns are exposed for now (timezone,
// locale, theme). The opaque preferences struct is left for the apps
// that own their own UI state to manage; surfacing it as a JSON editor
// here would invite blob-shaped corruption from typos.

// A small, well-known subset of IANA TZ ids, offered as suggestions on the
// timezone field. Any IANA name can still be typed.
const COMMON_TIMEZONES = [
  "UTC",
  "Europe/Kyiv",
  "Europe/London",
  "Europe/Berlin",
  "America/New_York",
  "America/Los_Angeles",
  "Asia/Tokyo",
  "Asia/Singapore",
  "Australia/Sydney",
];

// Same for locales: suggestions, and free-form BCP-47.
const COMMON_LOCALES = ["en-US", "en-GB", "uk-UA", "de-DE", "fr-FR", "ja-JP"];

// One width for every preference control, sized to its values rather than to
// the card: the longest common IANA name fits, and the column lines up.
const FIELD_WIDTH = "w-full sm:w-60";
const TIMEZONE_SUGGESTIONS = "prof-tz-suggestions";
const LOCALE_SUGGESTIONS = "prof-locale-suggestions";

export default function ProfilePage() {
  const { user } = useAuth();
  const { showNotification } = useNotification();

  const [saving, setSaving] = useState(false);

  // Form state — held separately from `settings` so the user can edit
  // freely without round-tripping the server, and we can compute a
  // "dirty" flag against the last-loaded baseline.
  const [timezone, setTimezone] = useState("");
  const [locale, setLocale] = useState("");
  const [theme, setTheme] = useState<string>(THEME_SYSTEM);

  const settingsQuery = useQuery({
    queryKey: USER_SETTINGS_QUERY_KEY,
    retry: false, // queryFn toasts real failures; NotFound is a normal state.
    queryFn: async ({ signal }) => {
      try {
        return await fetchMySettings(signal);
      } catch (err) {
        // An aborted query is not a failure the operator needs to see:
        // TanStack cancels in-flight reads on unmount and on supersede.
        if (isAbortError(err)) throw err;
        showNotification({
          type: "error",
          title: "Load failed",
          message: errorMessage(err, "Failed to load"),
        });
        throw err;
      }
    },
  });
  const settings = settingsQuery.data ?? null;
  const loading = settingsQuery.isFetching;
  // refetch is referentially stable across renders (TanStack), so it's safe
  // to list in the handleSave dependency array below.
  const loadMine = settingsQuery.refetch;

  // Seed the editable form whenever a new snapshot arrives — render-phase
  // adjust-on-change (not a set-state-in-effect hit). Identity is stable
  // between fetches via TanStack structural sharing.
  //
  // Starts at undefined, which no snapshot is (settings is null at worst), so
  // the first render seeds too. It started at `settings`, which was fine while
  // this page did the first read; once the shell read the same query, the
  // snapshot was already there on mount, nothing seeded, and Save would have
  // written empty fields over the saved ones.
  const [seededFrom, setSeededFrom] = useState<typeof settings | undefined>(
    undefined,
  );
  if (settings !== seededFrom) {
    setSeededFrom(settings);
    setTimezone(settings?.timezone || "");
    setLocale(settings?.locale || "");
    setTheme(settings?.theme || THEME_SYSTEM);
  }

  const dirty = useMemo(() => {
    if (!settings) {
      return Boolean(timezone || locale || theme !== THEME_SYSTEM);
    }
    return (
      timezone !== (settings.timezone || "") ||
      locale !== (settings.locale || "") ||
      theme !== (settings.theme || THEME_SYSTEM)
    );
  }, [settings, timezone, locale, theme]);

  const handleSave = useCallback(async () => {
    setSaving(true);
    try {
      await userSettingsClient.updateMine({
        updateMask: fieldMask(
          UpdateMineRequestSchema,
          "timezone",
          "locale",
          "theme",
        ),
        timezone,
        locale,
        theme,
        preferences: {},
      });
      await loadMine();
      showNotification({
        type: "success",
        title: "Saved",
        message: "Preferences synchronized to the control plane.",
      });
    } catch (err) {
      const msg = errorMessage(err, "Save failed");
      showNotification({
        type: "error",
        title: "Save failed",
        message: msg,
      });
    } finally {
      setSaving(false);
    }
  }, [timezone, locale, theme, showNotification, loadMine]);

  const handleReset = useCallback(() => {
    setTimezone(settings?.timezone || "");
    setLocale(settings?.locale || "");
    setTheme(settings?.theme || THEME_SYSTEM);
  }, [settings]);

  const detectedTz = useMemo(() => {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone;
    } catch {
      return "";
    }
  }, []);

  const detectedLocale = useMemo(() => {
    try {
      return Intl.DateTimeFormat().resolvedOptions().locale;
    } catch {
      return "";
    }
  }, []);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Profile"
        description="Personal preferences synced to the control plane."
        showDefaultActions={false}
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => void loadMine()}
            disabled={loading}
          >
            <ArrowPathIcon
              className={loading ? "size-4 animate-spin" : "size-4"}
            />
            Refresh
          </Button>
        }
      />

      {/* ─── Identity ────────────────────────────────────────────── */}
      <Card>
        <CardHeader className="flex flex-row items-center gap-3 px-6">
          <div className="flex size-9 items-center justify-center rounded-md bg-primary/15 text-primary ring-1 ring-primary/30">
            <UserCircleIcon className="size-5" />
          </div>
          <div className="flex-1 min-w-0">
            <CardTitle className="text-base">
              {user?.displayName || user?.subject || "—"}
            </CardTitle>
            <CardDescription className={T.code}>
              {user?.subject ? `subject ${user.subject}` : "no session"}
              {user?.tenantId
                ? ` · tenant ${user.tenantId.slice(0, 8)}…${user.tenantId.slice(-4)}`
                : ""}
            </CardDescription>
          </div>
          {user?.roles && user.roles.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {user.roles.map((r) => (
                <Badge
                  key={r}
                  variant="outline"
                  className={cn(T.labelTight, "font-mono")}
                >
                  {r}
                </Badge>
              ))}
            </div>
          )}
        </CardHeader>
      </Card>

      {/* ─── Preferences ─────────────────────────────────────────── */}
      <Card>
        <CardHeader className="px-6">
          <CardTitle className="text-base">Preferences</CardTitle>
          <CardDescription>
            {/* Theme and time zone apply (SettingsSync); locale deliberately
                does not (DISPLAY_LOCALE in lib/format/locale.ts). */}
            {SETTINGS_APPLIED_NOTE}
          </CardDescription>
        </CardHeader>
        <Separator />
        <CardContent className="space-y-5 px-6 py-5">
          {settingsQuery.isError && !settings ? (
            // The form used to show the defaults and "not saved yet" here,
            // and Save would have written them over the real settings.
            <ListLoadError
              what="Preferences"
              reason={errorMessage(settingsQuery.error)}
              onRetry={() => void settingsQuery.refetch()}
            />
          ) : loading ? (
            <div className="space-y-3">
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-full" />
            </div>
          ) : (
            <>
              {/* Theme */}
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-[160px_1fr] sm:items-center">
                <Label
                  htmlFor="prof-theme"
                  className="flex items-center gap-2 text-sm"
                >
                  <PaintBrushIcon className="size-4 text-muted-foreground" />
                  Theme
                </Label>
                <SelectRoot value={theme} onValueChange={setTheme}>
                  <SelectTrigger id="prof-theme" className={FIELD_WIDTH}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {THEME_OPTIONS.map((o) => (
                      <SelectItem key={o.value} value={o.value}>
                        {o.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </SelectRoot>
              </div>

              {/* Timezone */}
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-[160px_1fr] sm:items-start">
                <Label
                  htmlFor="prof-tz"
                  className="flex items-center gap-2 pt-2 text-sm"
                >
                  <ClockIcon className="size-4 text-muted-foreground" />
                  Timezone
                </Label>
                <div className="space-y-1.5">
                  <Input
                    id="prof-tz"
                    list={TIMEZONE_SUGGESTIONS}
                    placeholder={detectedTz || "Europe/Kyiv"}
                    value={timezone}
                    onChange={(e) => setTimezone(e.target.value)}
                    className={cn(FIELD_WIDTH, "font-mono text-xs")}
                  />
                  <datalist id={TIMEZONE_SUGGESTIONS}>
                    {COMMON_TIMEZONES.map((tz) => (
                      <option key={tz} value={tz} />
                    ))}
                  </datalist>
                  <p className={T.hint}>
                    IANA name. Detected from this browser:{" "}
                    <button
                      type="button"
                      onClick={() => detectedTz && setTimezone(detectedTz)}
                      className="font-mono text-primary hover:underline"
                    >
                      {detectedTz || "(unavailable)"}
                    </button>
                  </p>
                </div>
              </div>

              {/* Locale */}
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-[160px_1fr] sm:items-start">
                <Label
                  htmlFor="prof-locale"
                  className="flex items-center gap-2 pt-2 text-sm"
                >
                  <GlobeAltIcon className="size-4 text-muted-foreground" />
                  Locale
                </Label>
                <div className="space-y-1.5">
                  <Input
                    id="prof-locale"
                    list={LOCALE_SUGGESTIONS}
                    placeholder={detectedLocale || "en-US"}
                    value={locale}
                    onChange={(e) => setLocale(e.target.value)}
                    className={cn(FIELD_WIDTH, "font-mono text-xs")}
                  />
                  <datalist id={LOCALE_SUGGESTIONS}>
                    {COMMON_LOCALES.map((lo) => (
                      <option key={lo} value={lo} />
                    ))}
                  </datalist>
                  <p className={T.hint}>
                    BCP-47 tag. Detected from this browser:{" "}
                    <button
                      type="button"
                      onClick={() =>
                        detectedLocale && setLocale(detectedLocale)
                      }
                      className="font-mono text-primary hover:underline"
                    >
                      {detectedLocale || "(unavailable)"}
                    </button>
                  </p>
                </div>
              </div>

              {/* Footer */}
              <div className="flex items-center justify-between border-t pt-4">
                <div className={T.hint}>
                  {/* GetMine serves defaults to a user who never saved;
                      those carry no updatedAt. */}
                  {settings?.updatedAt ? (
                    <>
                      <CheckCircleIcon className="mr-1 inline-block size-3.5 align-text-bottom text-success" />
                      Last synced <Timestamp ts={settings.updatedAt} />
                      {" · resourceVersion "}
                      <span className="font-mono">
                        {settings.resourceVersion || "—"}
                      </span>
                    </>
                  ) : (
                    "Defaults — not saved yet."
                  )}
                </div>
                <div className="flex gap-2">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={handleReset}
                    disabled={!dirty || saving}
                  >
                    Reset
                  </Button>
                  <Button
                    size="sm"
                    onClick={handleSave}
                    disabled={!dirty || saving}
                  >
                    {saving ? "Saving…" : "Save changes"}
                  </Button>
                </div>
              </div>
            </>
          )}
        </CardContent>
      </Card>

      <ChangePasswordCard />
    </div>
  );
}
