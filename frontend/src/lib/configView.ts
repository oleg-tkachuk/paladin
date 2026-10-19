// The running control-plane configuration (SystemService.GetConfig), read as
// sections an operator can scan: one per top-level key, each a list of
// dotted paths and the value at each. The YAML is parsed with js-yaml, never
// split by hand.

import { dump, load } from "js-yaml";

/**
 * What the server writes in place of a secret (config.Redacted in
 * backend/internal/config/resolver.go; documented on GetConfigResponse.yaml).
 */
export const REDACTED = "***";

export type ConfigValue =
  | { kind: "text"; text: string }
  | { kind: "number"; text: string }
  | { kind: "bool"; value: boolean }
  | { kind: "empty" }
  | { kind: "redacted" }
  | { kind: "list"; items: string[] };

export interface ConfigEntry {
  /** The path below the section, "postgres.dsn"; "" for a scalar section. */
  path: string;
  value: ConfigValue;
}

export interface ConfigSection {
  key: string;
  entries: ConfigEntry[];
  /** The section as YAML, for copying it alone. */
  yaml: string;
}

const isMap = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v);

const scalarText = (v: unknown): string =>
  typeof v === "string" ? v : JSON.stringify(v);

function valueOf(v: unknown): ConfigValue {
  if (v === REDACTED) return { kind: "redacted" };
  if (v === null || v === undefined || v === "") return { kind: "empty" };
  if (typeof v === "boolean") return { kind: "bool", value: v };
  if (typeof v === "number") return { kind: "number", text: String(v) };
  if (Array.isArray(v)) {
    return v.length === 0
      ? { kind: "empty" }
      : { kind: "list", items: v.map(scalarText) };
  }
  return { kind: "text", text: String(v) };
}

// Maps are walked into dotted paths; a list stays one entry, a map item as
// JSON, since list items have no names to path by.
function flatten(v: unknown, prefix: string, out: ConfigEntry[]) {
  if (isMap(v) && Object.keys(v).length > 0) {
    for (const [k, child] of Object.entries(v)) {
      flatten(child, prefix ? `${prefix}.${k}` : k, out);
    }
    return;
  }
  out.push({ path: prefix, value: isMap(v) ? { kind: "empty" } : valueOf(v) });
}

/**
 * The configuration's sections, in the order the server wrote them; none for
 * an empty document. A document that is not YAML throws, so the page can show
 * it as text rather than as nothing.
 */
export function parseConfigSections(yaml: string): ConfigSection[] {
  if (!yaml.trim()) return [];
  const doc = load(yaml);
  if (!isMap(doc)) return [];
  return Object.entries(doc).map(([key, v]) => {
    const entries: ConfigEntry[] = [];
    flatten(v, "", entries);
    return { key, entries, yaml: dump({ [key]: v }) };
  });
}

/** How an entry's value reads, for a search. */
function entryText(e: ConfigEntry): string {
  const v = e.value;
  switch (v.kind) {
    case "text":
    case "number":
      return v.text;
    case "bool":
      return String(v.value);
    case "list":
      return v.items.join(" ");
    case "redacted":
      return REDACTED;
    case "empty":
      return "";
  }
}

/**
 * The sections and entries a search matches, by section name, path or value,
 * case-insensitively. A section whose name matches keeps every entry; one
 * with no matching entry is left out.
 */
export function filterConfigSections(
  sections: ConfigSection[],
  query: string,
): ConfigSection[] {
  const q = query.trim().toLowerCase();
  if (!q) return sections;
  return sections.flatMap((s) => {
    if (s.key.toLowerCase().includes(q)) return [s];
    const entries = s.entries.filter(
      (e) =>
        `${s.key}.${e.path}`.toLowerCase().includes(q) ||
        entryText(e).toLowerCase().includes(q),
    );
    return entries.length > 0 ? [{ ...s, entries }] : [];
  });
}

/** How many entries hold a redacted secret. */
export function redactedCount(sections: ConfigSection[]): number {
  return sections.reduce(
    (n, s) => n + s.entries.filter((e) => e.value.kind === "redacted").length,
    0,
  );
}
