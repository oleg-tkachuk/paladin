"use client";

import { useMemo, useState } from "react";
import {
  ArrowPathIcon,
  ClipboardDocumentIcon,
  ExclamationTriangleIcon,
  LockClosedIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useNotification } from "@/components/ui/Notification";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { Select } from "@/components/ui/Select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { DiffView } from "@/components/ui/DiffView";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { useConfig } from "@/hooks/useConfig";
import {
  comparableConfigs,
  filterConfigSections,
  parseConfigSections,
  redactedCount,
  withoutUnset,
  type ConfigSection,
  type ConfigValue,
} from "@/lib/configView";
import { T } from "@/lib/ui/typography";
import { copyToClipboard, cn } from "@/lib/utils";
import { FROSTED_MUTED, STICKY_BELOW_TOPBAR } from "@/lib/ui/states";

// /config — the running control plane's configuration, as
// admin/v1.SystemService.GetConfig returns it: the loaded config.yaml with
// every secret redacted by the server. Read as sections — one per top-level
// key, each setting a dotted path and its value — with a search over names
// and values; the YAML stays one toggle away for copying, and Compare diffs
// it against the defaults — what this deployment changed — or a config.yaml
// pasted in, such as the one checked in.

type View = "sections" | "yaml" | "compare";
const VIEWS: { value: View; label: string }[] = [
  { value: "sections", label: "Sections" },
  { value: "yaml", label: "YAML" },
  { value: "compare", label: "Compare" },
];

/**
 * The section picker's "every section". Not a key a config section can have:
 * the YAML top level is the config schema's, and none starts with "__".
 */
const ALL_SECTIONS = "__all__";

const plural = (n: number, one: string, many: string) =>
  `${n} ${n === 1 ? one : many}`;

export default function ConfigPage() {
  const { config, defaults, path, loading, error, refresh } = useConfig();
  const { showNotification } = useNotification();
  const [query, setQuery] = useState("");
  const [view, setView] = useState<View>("sections");
  const [section, setSection] = useState(ALL_SECTIONS);
  const [hideUnset, setHideUnset] = useState(false);

  // A document that is not YAML is shown as text, not as nothing.
  const parsed = useMemo(() => {
    try {
      return { sections: parseConfigSections(config ?? ""), invalid: false };
    } catch {
      return { sections: [] as ConfigSection[], invalid: true };
    }
  }, [config]);
  const shown = useMemo(() => {
    const picked =
      section === ALL_SECTIONS
        ? parsed.sections
        : parsed.sections.filter((s) => s.key === section);
    const matched = filterConfigSections(picked, query);
    return hideUnset ? withoutUnset(matched) : matched;
  }, [parsed.sections, section, query, hideUnset]);
  const sectionOptions = useMemo(
    () => [
      { value: ALL_SECTIONS, label: "All sections" },
      ...parsed.sections.map((s) => ({
        value: s.key,
        label: `${s.key} (${s.entries.length})`,
      })),
    ],
    [parsed.sections],
  );
  const total = parsed.sections.reduce((n, s) => n + s.entries.length, 0);
  const matching = shown.reduce((n, s) => n + s.entries.length, 0);
  const secrets = redactedCount(parsed.sections);
  const effectiveView: View = parsed.invalid ? "yaml" : view;

  const copy = async (text: string, what: string) => {
    const ok = await copyToClipboard(text);
    showNotification({
      type: ok ? "success" : "error",
      title: ok ? "Copied" : "Copy failed",
      message: ok
        ? `${what} copied to the clipboard.`
        : "Clipboard unavailable.",
    });
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Configuration"
        description="The control plane's running configuration, as loaded at boot. Secrets are redacted by the server; nothing here is editable."
        showDefaultActions={false}
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void refresh()}
              disabled={loading}
            >
              <ArrowPathIcon
                className={cn("size-4", loading && "animate-spin")}
              />
              Refresh
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void copy(config ?? "", "The configuration")}
              disabled={!config}
            >
              <ClipboardDocumentIcon className="size-4" />
              Copy all
            </Button>
          </>
        }
      />

      {error ? (
        <Card className="space-y-1 p-6 text-sm">
          <p className="flex items-center gap-2 font-medium text-destructive">
            <ExclamationTriangleIcon className="size-4" />
            The configuration could not be read
          </p>
          <p className={T.hint}>{error.message}</p>
          {/^permission/i.test(error.message) ? (
            <p className={T.hint}>
              It is shown to the{" "}
              <code className="font-mono">platform.admin</code> role only.
            </p>
          ) : null}
        </Card>
      ) : loading && !config ? (
        <div className="space-y-3">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-64 w-full" />
        </div>
      ) : (
        <>
          <p
            className={cn(
              T.hint,
              "flex flex-wrap items-center gap-x-2 gap-y-1",
            )}
          >
            <span className={T.codeSmall}>{path || "source unknown"}</span>
            <span aria-hidden>·</span>
            <span>
              {plural(parsed.sections.length, "section", "sections")},{" "}
              {plural(total, "setting", "settings")}
            </span>
            {secrets > 0 ? (
              <>
                <span aria-hidden>·</span>
                <span className="inline-flex items-center gap-1">
                  <LockClosedIcon className="size-3.5" />
                  {plural(secrets, "secret", "secrets")} redacted
                </span>
              </>
            ) : null}
          </p>

          <TableToolbar
            query={query}
            onQueryChange={setQuery}
            searchLabel="Search the configuration"
            placeholder="Search by setting or value…"
            filter={
              parsed.invalid
                ? undefined
                : {
                    label: "View",
                    options: VIEWS,
                    value: view,
                    onChange: setView,
                  }
            }
            count={
              effectiveView === "sections"
                ? { shown: matching, total }
                : undefined
            }
          >
            {effectiveView === "sections" ? (
              <>
                <Select
                  aria-label="Section"
                  value={section}
                  onChange={setSection}
                  options={sectionOptions}
                  className="w-48"
                />
                <label className={cn(T.hint, "flex items-center gap-2")}>
                  <Switch
                    checked={hideUnset}
                    onCheckedChange={setHideUnset}
                    aria-label="Hide unset"
                  />
                  Hide unset
                </label>
              </>
            ) : null}
          </TableToolbar>

          {parsed.invalid ? (
            <p className="flex items-center gap-2 text-sm text-warning">
              <ExclamationTriangleIcon className="size-4" />
              The server sent something that is not YAML; it is shown as it
              came.
            </p>
          ) : null}

          {effectiveView === "compare" ? (
            <CompareView running={config ?? ""} defaults={defaults} />
          ) : effectiveView === "yaml" ? (
            <Card className="p-0">
              <pre className="paladin-scroll max-h-[70vh] overflow-auto px-6 py-5 font-mono text-xs leading-relaxed">
                {config}
              </pre>
            </Card>
          ) : shown.length === 0 ? (
            <Card className="p-8 text-center text-sm text-muted-foreground">
              {query
                ? "No setting matches this search."
                : "The configuration is empty."}
            </Card>
          ) : (
            <Card className="gap-0 p-0">
              {shown.map((s) => (
                <SectionRows
                  key={s.key}
                  section={s}
                  onCopy={() => void copy(s.yaml, `Section ${s.key}`)}
                />
              ))}
            </Card>
          )}
        </>
      )}
    </div>
  );
}

/** What the running configuration is compared with. */
type CompareSource = "defaults" | "pasted";

/**
 * The running configuration diffed against the defaults — what this
 * deployment changed — or against one pasted in. Both sides are written out
 * alike first (comparableConfigs), so the diff holds settings that differ,
 * not comments or key order. Nothing pasted is sent anywhere.
 */
function CompareView({
  running,
  defaults,
}: {
  running: string;
  defaults: string | null;
}) {
  const [source, setSource] = useState<CompareSource>(
    defaults ? "defaults" : "pasted",
  );
  const [pasted, setPasted] = useState("");
  const other = source === "defaults" ? (defaults ?? "") : pasted;
  const compared = useMemo(() => {
    if (!other.trim()) return null;
    try {
      return comparableConfigs(running, other);
    } catch {
      return "invalid" as const;
    }
  }, [running, other]);
  return (
    <Card className="space-y-3 p-4">
      <div role="group" aria-label="Compare against" className="flex gap-1">
        {(
          [
            ["defaults", "Defaults"],
            ["pasted", "A pasted config.yaml"],
          ] as const
        ).map(([value, label]) => (
          <Button
            key={value}
            type="button"
            size="sm"
            variant={source === value ? "secondary" : "ghost"}
            aria-pressed={source === value}
            disabled={value === "defaults" && !defaults}
            onClick={() => setSource(value)}
          >
            {label}
          </Button>
        ))}
      </div>
      {source === "defaults" ? (
        <p className={T.hint}>
          What this deployment changed: every setting the schema defaults,
          beside the value running. A setting with no default shows as set.
        </p>
      ) : (
        <div className="space-y-1.5">
          <Label htmlFor="config-compare">Compare with</Label>
          <Textarea
            id="config-compare"
            value={pasted}
            onChange={(e) => setPasted(e.target.value)}
            placeholder="Paste a config.yaml, such as the one checked in"
            className="min-h-32 font-mono text-xs"
            spellCheck={false}
          />
          <p className={T.hint}>
            Comments, quoting and key order are not counted, and a secret the
            server redacts is redacted in the pasted copy too.
          </p>
        </div>
      )}
      {compared === "invalid" ? (
        <p className="flex items-center gap-2 text-sm text-warning">
          <ExclamationTriangleIcon className="size-4" />
          The pasted text is not YAML.
        </p>
      ) : compared ? (
        <DiffView
          before={compared.other}
          after={compared.running}
          beforeLabel={source === "defaults" ? "Defaults" : "Pasted"}
          afterLabel="Running"
        />
      ) : null}
    </Card>
  );
}

function SectionRows({
  section,
  onCopy,
}: {
  section: ConfigSection;
  onCopy: () => void;
}) {
  const titleId = `config-${section.key}-title`;
  return (
    <section aria-labelledby={titleId} className="border-b last:border-b-0">
      <div
        className={cn(
          STICKY_BELOW_TOPBAR,
          FROSTED_MUTED,
          "flex items-center justify-between gap-3 border-b px-4 py-2",
        )}
      >
        <h2 id={titleId} className="font-mono text-sm font-semibold">
          {section.key}
          <span className="ml-2 font-sans text-xs font-normal text-muted-foreground">
            {section.entries.length}
          </span>
        </h2>
        <Button
          variant="ghost"
          size="sm"
          className="h-7 px-2 text-xs"
          onClick={onCopy}
          aria-label={`Copy section ${section.key}`}
        >
          <ClipboardDocumentIcon className="size-3.5" />
          Copy
        </Button>
      </div>
      <dl>
        {section.entries.map((e) => (
          <div
            key={e.path || section.key}
            className="grid gap-1 px-4 py-1.5 hover:bg-muted/30 sm:grid-cols-[minmax(12rem,20rem)_1fr] sm:gap-6"
          >
            <dt className={cn(T.codeSmall, "break-all text-muted-foreground")}>
              {e.path || section.key}
            </dt>
            <dd className="min-w-0">
              <ValueView value={e.value} />
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function ValueView({ value }: { value: ConfigValue }) {
  switch (value.kind) {
    case "redacted":
      return (
        <Badge variant="outline" className="gap-1 font-normal">
          <LockClosedIcon className="size-3" />
          secret · redacted
        </Badge>
      );
    case "empty":
      return <span className={cn(T.hint, "italic")}>not set</span>;
    case "bool":
      return (
        <span
          className={cn(
            T.codeSmall,
            value.value ? "text-success" : "text-muted-foreground",
          )}
        >
          {String(value.value)}
        </span>
      );
    case "list":
      return (
        <span className="flex flex-wrap gap-1">
          {value.items.map((item, i) => (
            <code
              key={`${i}-${item}`}
              className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs break-all"
            >
              {item}
            </code>
          ))}
        </span>
      );
    case "number":
      return (
        <span className={cn(T.codeSmall, "tabular-nums")}>{value.text}</span>
      );
    case "text":
      return <span className={cn(T.codeSmall, "break-all")}>{value.text}</span>;
  }
}
