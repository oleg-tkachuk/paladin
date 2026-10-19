"use client";

import { Confirmation } from "@/components/ui/Confirmation";
import { useConfirmation } from "@/hooks/useConfirmation";
import { NotifyOperationsField } from "@/components/features/NotifyOperationsField";
import { ResourceLinksField } from "@/components/features/ResourceLinksField";
import { useCallback, useMemo, useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { CheckCircleIcon, KeyIcon } from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { SettingsPage } from "@/components/layout/templates/SettingsPage";
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
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { useNotification } from "@/components/ui/Notification";
import { ChangePasswordCard } from "@/components/features/ChangePasswordCard";
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
  THEME_OPTIONS,
  THEME_SYSTEM,
  USER_SETTINGS_QUERY_KEY,
} from "@/lib/theme";
import { COMMON_LOCALES, timeZoneOptions } from "@/lib/userPrefs";
import {
  isSameLocale,
  isSameTimeZone,
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
    <SettingsPage
      header={
        <PageHeader
          title="Profile"
          description="Your account, how the console shows things to you, and your password."
          showDefaultActions={false}
        />
      }
      summary={<AccountCard />}
    >
      <PreferencesCard />
      <ChangePasswordCard />
    </SettingsPage>
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
    <Card className="py-0">
      <CardContent className="grid items-center gap-x-8 gap-y-5 px-6 py-4 sm:grid-cols-2 lg:grid-cols-[minmax(12rem,auto)_minmax(0,1fr)_minmax(0,1.2fr)_minmax(0,1.6fr)_auto]">
        <div className="flex min-w-0 items-center gap-3">
          <span
            aria-hidden
            className="flex size-10 shrink-0 items-center justify-center rounded-full bg-primary/15 font-semibold text-primary ring-1 ring-primary/30"
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

        <AccountField label="Tenant">
          {linkTenant ? (
            <Link
              href={tenantHref(tenantHandle)}
              className="truncate hover:text-primary hover:underline"
            >
              {tenantName}
            </Link>
          ) : (
            <span className="truncate">{tenantName ?? "—"}</span>
          )}
        </AccountField>

        <AccountField label="Roles">
          {roles.length === 0 ? (
            <span className={T.hint}>none</span>
          ) : (
            <span className="flex flex-wrap gap-1">
              {roles.map((r) => (
                <Badge
                  key={r}
                  variant="outline"
                  className="font-mono"
                  title={ROLE_DESCRIPTIONS[r]}
                >
                  {r}
                </Badge>
              ))}
            </span>
          )}
        </AccountField>

        {user?.userId ? (
          <AccountField label="User ID">
            <span className="flex min-w-0 items-center gap-1.5">
              <span className={cn(T.codeSmall, "truncate")} title={user.userId}>
                {user.userId}
              </span>
              <IdentifierCopy
                value={user.userId}
                label="User ID"
                iconOnly
                className="shrink-0 p-1"
              />
            </span>
          </AccountField>
        ) : (
          <span />
        )}

        <Link
          href={SECTION.applications}
          className="flex items-center gap-1.5 text-sm text-primary hover:underline lg:justify-self-end"
        >
          <KeyIcon className="size-4" />
          Connected applications
        </Link>
      </CardContent>
    </Card>
  );
}

/** One fact of the account bar: a label over its value. */
function AccountField({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0 space-y-1">
      <p className={T.label}>{label}</p>
      <div className="flex min-w-0 text-sm">{children}</div>
    </div>
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

  // Confirmed beside the Save button, where it was pressed.
  const { shown: saved, confirm: confirmSaved } = useConfirmation();
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
      confirmSaved();
    } catch (err) {
      showNotification({
        type: "error",
        title: "Save failed",
        message: errorMessage(err, "Save failed"),
      });
    } finally {
      setSaving(false);
    }
  }, [timezone, locale, theme, showNotification, loadMine, confirmSaved]);

  const handleReset = useCallback(() => {
    setTimezone(settings?.timezone || "");
    setLocale(settings?.locale || "");
    setTheme(settings?.theme || THEME_SYSTEM);
  }, [settings]);

  return (
    <Card className="h-full">
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
            <div className="space-y-5">
              <div className="space-y-1.5">
                <Label htmlFor="prof-theme">Theme</Label>
                <SelectRoot value={theme} onValueChange={setTheme}>
                  <SelectTrigger id="prof-theme" className="w-full">
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
                <p className={T.hint}>Applies as soon as it is saved.</p>
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
                sameAsDetected={isSameTimeZone(timezone, detectedTz)}
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
                sameAsDetected={isSameLocale(locale, detectedLocale)}
                hint={LOCALE_NOT_APPLIED_NOTE}
              />
            </div>

            <div className="space-y-4 border-t pt-4">
              <NotifyOperationsField />
              <ResourceLinksField />
            </div>
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
                <Confirmation shown={saved}>Saved</Confirmation>
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
  sameAsDetected,
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
  /** Whether the value is already this browser's, if under another name. */
  sameAsDetected: boolean;
  hint: string;
}) {
  const statusId = `${id}-status`;
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between gap-2">
        <Label htmlFor={id}>{label}</Label>
        {detected && !sameAsDetected ? (
          <button
            type="button"
            onClick={() => onChange(detected)}
            className="truncate text-xs text-primary hover:underline"
            aria-label={`Use this browser's (${detected})`}
          >
            Use {detected}
          </button>
        ) : null}
      </div>
      <Input
        id={id}
        list={listId}
        value={value}
        placeholder={detected}
        onChange={(e) => onChange(e.target.value.trim())}
        aria-invalid={!valid || undefined}
        aria-describedby={statusId}
        className="font-mono text-xs"
      />
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
  );
}
