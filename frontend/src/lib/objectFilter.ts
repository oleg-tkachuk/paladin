// Pure CEL-filter construction for the Collection objects page. No React —
// the hook owns the values and their URL sync, this turns them into the one
// expression the list RPC takes.
//
// Every clause here is one the server pushes into SQL (an index or an indexed
// function per clause), and they are joined with `&&` only: a conjunction of
// pushable clauses is one indexed query, which is what keeps a selective
// filter from scanning the collection page by page.
import { celString } from "@/lib/cel";

/**
 * Content-type categories offered in the filter bar. The URL carries the
 * `value`; anything else in the URL is ignored rather than spliced into the
 * expression, so a link cannot make the console send a filter it would not.
 */
export const CONTENT_TYPE_OPTIONS = [
  { value: "image", label: "Images", prefix: "image/" },
  { value: "video", label: "Video", prefix: "video/" },
  { value: "audio", label: "Audio", prefix: "audio/" },
  { value: "text", label: "Text", prefix: "text/" },
  { value: "pdf", label: "PDF", exact: "application/pdf" },
  { value: "json", label: "JSON", exact: "application/json" },
] as const;

export function contentTypeClause(
  value: string | undefined,
): string | undefined {
  const opt = CONTENT_TYPE_OPTIONS.find((o) => o.value === value);
  if (!opt) return undefined;
  return "exact" in opt
    ? `content_type == ${celString(opt.exact)}`
    : `content_type.startsWith(${celString(opt.prefix)})`;
}

/**
 * Split a "key=value" facet on its FIRST "=" — values may contain "=".
 * Undefined when there is no "=" or the key is empty. Both halves are taken
 * exactly: a tag key with a space in it is a different key.
 */
export function parseKeyValue(
  raw: string | undefined,
): { key: string; value: string } | undefined {
  if (!raw) return undefined;
  const eq = raw.indexOf("=");
  if (eq <= 0) return undefined;
  return { key: raw.slice(0, eq), value: raw.slice(eq + 1) };
}

export interface ObjectFilterInput {
  status?: string;
  /** Search text; only applied from three characters, as the server's
   *  trigram index needs. */
  search?: string;
  /** Tag facet, "key=value". */
  tag?: string;
  /** A CONTENT_TYPE_OPTIONS value. */
  type?: string;
  /** Metadata facet, "key=value". */
  meta?: string;
}

/** The CEL filter for the object list; "" when nothing is filtered. */
export function buildObjectFilter(f: ObjectFilterInput): string {
  const parts: string[] = [];
  if (f.status) parts.push(`state == ${celString(f.status)}`);
  if (f.search && f.search.length > 2)
    parts.push(`key.contains(${celString(f.search)})`);
  const tag = parseKeyValue(f.tag);
  if (tag) parts.push(`tags[${celString(tag.key)}] == ${celString(tag.value)}`);
  const type = contentTypeClause(f.type);
  if (type) parts.push(type);
  const meta = parseKeyValue(f.meta);
  if (meta)
    parts.push(`metadata[${celString(meta.key)}] == ${celString(meta.value)}`);
  return parts.join(" && ");
}
