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
import { TableToolbar } from "@/components/ui/TableToolbar";
import { useConfig } from "@/hooks/useConfig";
import {
  filterConfigSections,
  parseConfigSections,
  redactedCount,
  type ConfigSection,
  type ConfigValue,
} from "@/lib/configView";
import { T } from "@/lib/ui/typography";
import { copyToClipboard, cn } from "@/lib/utils";

// /config — the running control plane's configuration, as
// admin/v1.SystemService.GetConfig returns it: the loaded config.yaml with
// every secret redacted by the server. Read as sections — one per top-level
// key, each setting a dotted path and its value — with a search over names
// and values; the YAML stays one toggle away for copying or diffing.

type View = "sections" | "yaml";
const VIEWS: { value: View; label: string }[] = [
  { value: "sections", label: "Sections" },
  { value: "yaml", label: "YAML" },
];

/** The anchor a section is reached at from the section list. */
const sectionAnchor = (key: string) => `config-${key}`;

const plural = (n: number, one: string, many: string) =>
  `${n} ${n === 1 ? one : many}`;

export default function ConfigPage() {
  const { config, path, loading, error, refresh } = useConfig();
  const { showNotification } = useNotification();
  const [query, setQuery] = useState("");
  const [view, setView] = useState<View>("sections");

  // A document that is not YAML is shown as text, not as nothing.
  const parsed = useMemo(() => {
    try {
      return { sections: parseConfigSections(config ?? ""), invalid: false };
    } catch {
      return { sections: [] as ConfigSection[], invalid: true };
    }
  }, [config]);
  const shown = useMemo(
    () => filterConfigSections(parsed.sections, query),
    [parsed.sections, query],
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
          />

          {parsed.invalid ? (
            <p className="flex items-center gap-2 text-sm text-warning">
              <ExclamationTriangleIcon className="size-4" />
              The server sent something that is not YAML; it is shown as it
              came.
            </p>
          ) : null}

          {effectiveView === "yaml" ? (
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
            <div className="grid gap-6 lg:grid-cols-[12rem_1fr]">
              <nav aria-label="Sections" className="hidden lg:block">
                <ul className="sticky top-4 space-y-0.5">
                  {shown.map((s) => (
                    <li key={s.key}>
                      <a
                        href={`#${sectionAnchor(s.key)}`}
                        className="flex items-center justify-between rounded-md px-2 py-1 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
                      >
                        <span className={T.codeSmall}>{s.key}</span>
                        <span className="tabular-nums text-xs">
                          {s.entries.length}
                        </span>
                      </a>
                    </li>
                  ))}
                </ul>
              </nav>
              <div className="min-w-0 space-y-4">
                {shown.map((s) => (
                  <SectionCard
                    key={s.key}
                    section={s}
                    onCopy={() => void copy(s.yaml, `Section ${s.key}`)}
                  />
                ))}
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function SectionCard({
  section,
  onCopy,
}: {
  section: ConfigSection;
  onCopy: () => void;
}) {
  return (
    <section
      id={sectionAnchor(section.key)}
      aria-labelledby={`${sectionAnchor(section.key)}-title`}
      className="scroll-mt-4"
    >
      <Card className="p-0">
        <div className="flex items-center justify-between gap-3 border-b px-4 py-2.5">
          <h2
            id={`${sectionAnchor(section.key)}-title`}
            className="font-mono text-sm font-semibold"
          >
            {section.key}
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
        <dl className="divide-y">
          {section.entries.map((e) => (
            <div
              key={e.path || section.key}
              className="grid gap-1 px-4 py-2 sm:grid-cols-[minmax(12rem,2fr)_3fr] sm:gap-4"
            >
              <dt
                className={cn(T.codeSmall, "break-all text-muted-foreground")}
              >
                {e.path || section.key}
              </dt>
              <dd className="min-w-0">
                <ValueView value={e.value} />
              </dd>
            </div>
          ))}
        </dl>
      </Card>
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
        <Badge variant={value.value ? "success" : "secondary"}>
          {String(value.value)}
        </Badge>
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
