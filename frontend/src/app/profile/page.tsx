"use client";

import { useCallback, useMemo, useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { CheckCircleIcon, KeyIcon } from "@heroicons/react/24/outline";

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
import { Skeleton } from "@/components/ui/Skeleton";
import { IdentifierCopy } from "@/components/ui/IdentifierCopy";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { useNotification } from "@/components/ui/Notification";
import { ChangePasswordCard } from "@/components/features/ChangePasswordCard";
import { ThemePicker } from "@/components/features/profile/ThemePicker";
import { Timestamp } from "@/components/Timestamp";
import { useAuth } from "@/context/AuthContext";
import { useScope } from "@/context/ScopeContext";
import { canUseAdminPlane, ROLE_DESCRIPTIONS } from "@/constants/roles";
import { UpdateMineRequestSchema } from "@/gen/paladin/iam/v1/user_settings_service_pb";
import { isAbortError, errorMessage } from "@/hooks/errorContract";
import { userSettingsClient } from "@/lib/connect/client";
import { fieldMask } from "@/lib/connect/fieldMask";
import { SECTION } from "@/lib/navigation/sections";
import { tenantHref } from "@/lib/navigation/hrefs";
import { sessionTenantLabel } from "@/lib/tenant/sessionTenantLabel";
import {
  fetchMySettings,
  THEME_SYSTEM,
  USER_SETTINGS_QUERY_KEY,
} from "@/lib/theme";
import { COMMON_LOCALES, timeZoneOptions } from "@/lib/userPrefs";
import {
  isValidLocale,
  isValidTimeZone,
  localePreview,
  timeZonePreview,
} from "@/lib/format/preferences";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";
import { LOCALE_NOT_APPLIED_NOTE } from "./_constants";

// /profile — the signed-in user's account and preferences
// (iam/v1.UserSettingsService GetMine / UpdateMine, AuthService for the
// password). Everything here is the caller's own row, so it works for a
// tenant.user too: nothing on this page reads the admin plane.

const TIMEZONE_SUGGESTIONS = "prof-tz-suggestions";
const LOCALE_SUGGESTIONS = "prof-locale-suggestions";
const THEME_LABEL = "prof-theme-label";
const FIELD_WIDTH = "w-full sm:w-72";

function browserZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch {
    return "";
  }
}

function browserLocale(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().locale;
  } catch {
    return "";
  }
}

export default function ProfilePage() {
  return (
    <div className="space-y-6">
      <PageHeader
        title="Profile"
        description="Your account, how the console shows things to you, and your password."
        showDefaultActions={false}
      />
      <div className="grid items-start gap-6 xl:grid-cols-[20rem_1fr]">
        <AccountCard />
        <div className="min-w-0 space-y-6">
          <PreferencesCard />
          <ChangePasswordCard />
        </div>
      </div>
    </div>
  );
}

function AccountCard() {
  const { user } = useAuth();
  const { tenant } = useScope();
  const roles = user?.roles ?? [];
  const name = user?.displayName || user?.subject || "—";
  const tenantName = sessionTenantLabel({
    displayName: tenant?.displayName,
    slug: user?.tenantSlug,
    id: user?.tenantId,
  });
  const tenantHandle = user?.tenantSlug || user?.tenantId;
  // A tenant.user has no admin plane, so a link to the workspace would open
  // on refusals; the name alone says which tenant it is.
  const linkTenant = tenantHandle && canUseAdminPlane(roles);

  return (
    <Card>
      <CardContent className="space-y-5 px-6 py-5">
        <div className="flex items-center gap-3">
          <span
            aria-hidden
            className="flex size-11 items-center justify-center rounded-full bg-primary/15 text-lg font-semibold text-primary ring-1 ring-primary/30"
          >
            {name.slice(0, 1).toUpperCase()}
          </span>
          <div className="min-w-0">
            <p className="truncate font-semibold">{name}</p>
            <p className={cn(T.hint, "truncate")}>
              signed in as <span className="font-mono">{user?.subject}</span>
            </p>
          </div>
        </div>

        <dl className="space-y-4 text-sm">
          <div className="space-y-1">
            <dt className={T.label}>Tenant</dt>
            <dd>
              {linkTenant ? (
                <Link
                  href={tenantHref(tenantHandle)}
                  className="hover:text-primary hover:underline"
                >
                  {tenantName}
                </Link>
              ) : (
                (tenantName ?? "—")
              )}
            </dd>
          </div>
          <div className="space-y-1.5">
            <dt className={T.label}>Roles</dt>
            <dd>
              {roles.length === 0 ? (
                <span className={T.hint}>none</span>
              ) : (
                <ul className="space-y-2">
                  {roles.map((r) => (
                    <li key={r} className="space-y-0.5">
                      <Badge variant="outline" className="font-mono">
                        {r}
                      </Badge>
                      {ROLE_DESCRIPTIONS[r] ? (
                        <p className={T.hint}>{ROLE_DESCRIPTIONS[r]}</p>
                      ) : null}
                    </li>
                  ))}
                </ul>
              )}
            </dd>
          </div>
          {user?.userId ? (
            <div className="space-y-1">
              <dt className={T.label}>User ID</dt>
              <dd>
                <IdentifierCopy value={user.userId} label="User ID" />
              </dd>
            </div>
          ) : null}
        </dl>

        <Link
          href={SECTION.applications}
          className="flex items-center gap-2 text-sm text-primary hover:underline"
        >
          <KeyIcon className="size-4" />
          Connected applications
        </Link>
      </CardContent>
    </Card>
  );
}

function PreferencesCard() {
  const { showNotification } = useNotification();
  const [saving, setSaving] = useState(false);
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
  const loadMine = settingsQuery.refetch;

  // Seed the form whenever a new snapshot arrives — render-phase
  // adjust-on-change. Starts at undefined, which no snapshot is, so the first
  // render seeds too: the shell reads the same query, and the snapshot is
  // usually cached before this page mounts.
  const [seededFrom, setSeededFrom] = useState<typeof settings | undefined>(
    undefined,
  );
  if (settings !== seededFrom) {
    setSeededFrom(settings);
    setTimezone(settings?.timezone || "");
    setLocale(settings?.locale || "");
    setTheme(settings?.theme || THEME_SYSTEM);
  }

  const dirty = useMemo(
    () =>
      timezone !== (settings?.timezone || "") ||
      locale !== (settings?.locale || "") ||
      theme !== (settings?.theme || THEME_SYSTEM),
    [settings, timezone, locale, theme],
  );
  const tzValid = isValidTimeZone(timezone);
  const localeValid = isValidLocale(locale);
  const canSave = dirty && tzValid && localeValid && !saving;

  const zones = useMemo(() => timeZoneOptions(), []);
  const detectedTz = useMemo(() => browserZone(), []);
  const detectedLocale = useMemo(() => browserLocale(), []);
  const now = new Date();

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
        message: "Your preferences are saved.",
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Save failed",
        message: errorMessage(err, "Save failed"),
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

  return (
    <Card>
      <CardHeader className="px-6">
        <CardTitle className="text-base">Preferences</CardTitle>
        <CardDescription>
          Saved to your account, so every browser you sign in from shows the
          same.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-6 px-6 pb-5">
        {settingsQuery.isError && !settings ? (
          // Showing the defaults here would let Save write them over the
          // real settings.
          <ListLoadError
            what="Preferences"
            reason={errorMessage(settingsQuery.error)}
            onRetry={() => void settingsQuery.refetch()}
          />
        ) : settingsQuery.isFetching && !settings ? (
          <div className="space-y-3">
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-9 w-72" />
            <Skeleton className="h-9 w-72" />
          </div>
        ) : (
          <>
            <div className="space-y-2">
              <p id={THEME_LABEL} className="text-sm font-medium">
                Theme
              </p>
              <ThemePicker
                value={theme}
                onChange={setTheme}
                labelledBy={THEME_LABEL}
              />
            </div>

            <PreferenceField
              id="prof-tz"
              label="Timezone"
              listId={TIMEZONE_SUGGESTIONS}
              suggestions={zones}
              value={timezone}
              onChange={setTimezone}
              valid={tzValid}
              invalidText={
                timezone ? "Not a time zone this browser knows." : "Required."
              }
              preview={
                timeZonePreview(timezone, now) &&
                `Now there: ${timeZonePreview(timezone, now)}`
              }
              detected={detectedTz}
              hint="Dates and times in the console are shown in this zone."
            />

            <PreferenceField
              id="prof-locale"
              label="Locale"
              listId={LOCALE_SUGGESTIONS}
              suggestions={COMMON_LOCALES}
              value={locale}
              onChange={setLocale}
              valid={localeValid}
              invalidText={
                locale ? "Not a BCP-47 tag, such as en-US." : "Required."
              }
              preview={
                localePreview(locale, now) &&
                `Writes ${localePreview(locale, now)}`
              }
              detected={detectedLocale}
              hint={LOCALE_NOT_APPLIED_NOTE}
            />

            <div className="flex flex-wrap items-center justify-between gap-3 border-t pt-4">
              <p className={T.hint}>
                {/* GetMine serves defaults to a user who never saved; those
                    carry no updatedAt. */}
                {settings?.updatedAt ? (
                  <>
                    <CheckCircleIcon className="mr-1 inline-block size-3.5 align-text-bottom text-success" />
                    Saved <Timestamp ts={settings.updatedAt} />
                  </>
                ) : (
                  "Not saved yet — these are the defaults."
                )}
              </p>
              <div className="flex gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={handleReset}
                  disabled={!dirty || saving}
                >
                  Reset
                </Button>
                <Button size="sm" onClick={handleSave} disabled={!canSave}>
                  {saving ? "Saving…" : "Save changes"}
                </Button>
              </div>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}

function PreferenceField({
  id,
  label,
  listId,
  suggestions,
  value,
  onChange,
  valid,
  invalidText,
  preview,
  detected,
  hint,
}: {
  id: string;
  label: string;
  listId: string;
  suggestions: readonly string[];
  value: string;
  onChange: (v: string) => void;
  valid: boolean;
  invalidText: string;
  preview: string | null;
  detected: string;
  hint: string;
}) {
  const statusId = `${id}-status`;
  return (
    <div className="grid gap-2 sm:grid-cols-[8rem_1fr] sm:items-start">
      <Label htmlFor={id} className="pt-2 text-sm">
        {label}
      </Label>
      <div className="space-y-1.5">
        <div className="flex flex-wrap items-center gap-2">
          <Input
            id={id}
            list={listId}
            value={value}
            placeholder={detected}
            onChange={(e) => onChange(e.target.value.trim())}
            aria-invalid={!valid || undefined}
            aria-describedby={statusId}
            className={cn(FIELD_WIDTH, "font-mono text-xs")}
          />
          {detected && detected !== value ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-7 px-2 text-xs"
              onClick={() => onChange(detected)}
            >
              Use this browser&apos;s ({detected})
            </Button>
          ) : null}
        </div>
        <datalist id={listId}>
          {suggestions.map((s) => (
            <option key={s} value={s} />
          ))}
        </datalist>
        <p
          id={statusId}
          className={cn(
            "text-xs",
            valid ? "text-foreground" : "text-destructive",
          )}
        >
          {valid ? preview : invalidText}
        </p>
        <p className={T.hint}>{hint}</p>
      </div>
    </div>
  );
}
