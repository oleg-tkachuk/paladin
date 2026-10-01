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
import { ConnectError, Code } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";

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
  Select,
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
import { formatTimestampUTC } from "@/lib/format/timestamp";
import { SETTINGS_NOT_APPLIED } from "./_constants";

// /profile — self-service editor backed by iam/v1.UserSettingsService.
// Tenant comes from the JWT, so the page always operates on the calling
// principal's row (GetMine / UpdateMine) — no Cedar round-trip needed.
//
// Only the three first-class columns are exposed for now (timezone,
// locale, theme). The opaque preferences struct is left for the apps
// that own their own UI state to manage; surfacing it as a JSON editor
// here would invite blob-shaped corruption from typos.

const THEME_OPTIONS = [
  { value: "system", label: "Match system" },
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
];

// A small, well-known subset of IANA TZ ids. Operators who need a
// timezone outside this list can type any IANA name into the input
// (the field is editable text); the dropdown is just a quick-pick.
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

// Same approach for locales — a curated quick-pick + free-form text.
const COMMON_LOCALES = ["en-US", "en-GB", "uk-UA", "de-DE", "fr-FR", "ja-JP"];

export default function ProfilePage() {
  const { user } = useAuth();
  const { showNotification } = useNotification();

  const [saving, setSaving] = useState(false);

  // Form state — held separately from `settings` so the user can edit
  // freely without round-tripping the server, and we can compute a
  // "dirty" flag against the last-loaded baseline.
  const [timezone, setTimezone] = useState("");
  const [locale, setLocale] = useState("");
  const [theme, setTheme] = useState("system");

  const settingsQuery = useQuery({
    queryKey: ["userSettings"],
    retry: false, // queryFn toasts real failures; NotFound is a normal state.
    queryFn: async ({ signal }) => {
      try {
        return await userSettingsClient.getMine({}, { signal });
      } catch (err) {
        // An aborted query is not a failure the operator needs to see:
        // TanStack cancels in-flight reads on unmount and on supersede.
        if (isAbortError(err)) throw err;
        // First-time users may not have a row yet; the backend creates an
        // empty default on UpdateMine, so a NotFound here is fine → null.
        // Decided on the code, not by matching the message text.
        if (err instanceof ConnectError && err.code === Code.NotFound) {
          return null;
        }
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
  const [seededFrom, setSeededFrom] = useState(settings);
  if (settings !== seededFrom) {
    setSeededFrom(settings);
    setTimezone(settings?.timezone || "");
    setLocale(settings?.locale || "");
    setTheme(settings?.theme || "system");
  }

  const dirty = useMemo(() => {
    if (!settings) {
      return Boolean(timezone || locale || theme !== "system");
    }
    return (
      timezone !== (settings.timezone || "") ||
      locale !== (settings.locale || "") ||
      theme !== (settings.theme || "system")
    );
  }, [settings, timezone, locale, theme]);

  const handleSave = useCallback(async () => {
    setSaving(true);
    try {
      await userSettingsClient.updateMine({
        updateMask: create(FieldMaskSchema, {
          paths: ["timezone", "locale", "theme"],
        }),
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
    setTheme(settings?.theme || "system");
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
            {/* Nothing in the console reads these back (see BACKLOG); saying
                so beats a Theme picker that changes nothing. */}
            {SETTINGS_NOT_APPLIED}
          </CardDescription>
        </CardHeader>
        <Separator />
        <CardContent className="space-y-5 px-6 py-5">
          {loading ? (
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
                  <SelectTrigger id="prof-theme" className="w-full sm:w-60">
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
                  <div className="flex flex-col gap-2 sm:flex-row">
                    <Select
                      aria-label="Common timezones"
                      options={COMMON_TIMEZONES.map((tz) => ({
                        value: tz,
                        label: tz,
                      }))}
                      value={
                        COMMON_TIMEZONES.includes(timezone) ? timezone : ""
                      }
                      onChange={setTimezone}
                      placeholder="— pick —"
                      className="w-full sm:w-60"
                    />
                    <Input
                      id="prof-tz"
                      placeholder={detectedTz || "Europe/Kyiv"}
                      value={timezone}
                      onChange={(e) => setTimezone(e.target.value)}
                      className="font-mono text-xs"
                    />
                  </div>
                  <p className={T.hint}>
                    IANA name. Empty = server default. Detected from this
                    browser:{" "}
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
                  <div className="flex flex-col gap-2 sm:flex-row">
                    <Select
                      aria-label="Common locales"
                      options={COMMON_LOCALES.map((lo) => ({
                        value: lo,
                        label: lo,
                      }))}
                      value={COMMON_LOCALES.includes(locale) ? locale : ""}
                      onChange={setLocale}
                      placeholder="— pick —"
                      className="w-full sm:w-60"
                    />
                    <Input
                      id="prof-locale"
                      placeholder={detectedLocale || "en-US"}
                      value={locale}
                      onChange={(e) => setLocale(e.target.value)}
                      className="font-mono text-xs"
                    />
                  </div>
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
                      <CheckCircleIcon className="mr-1 inline-block size-3.5 align-text-bottom text-emerald-500" />
                      Last synced {formatTimestampUTC(settings.updatedAt)}
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
