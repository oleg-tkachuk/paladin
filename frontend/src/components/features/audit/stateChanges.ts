// What an audited action changed in a resource: its state before and after,
// as the server recorded them (AuditLogEntry.state_before_json and
// state_after_json, secrets redacted), compared field by field. Pure, so the
// panel only lays out what this computes.

/** How a field differs between the two states. */
export type FieldChangeKind = "added" | "removed" | "changed";

export interface FieldChange {
  /** The field's path: dotted for a nested field, indexed for a list item. */
  path: string;
  kind: FieldChangeKind;
  /** The value before, as JSON; absent for an added field. */
  before?: string;
  /** The value after, as JSON; absent for a removed field. */
  after?: string;
}

/** What the state of an entry shows, side by side. */
export type StateChange =
  | { kind: "none" }
  | { kind: "created"; after: unknown }
  | { kind: "removed"; before: unknown }
  | { kind: "changed"; fields: FieldChange[] };

/** A state as the server stored it, parsed; undefined for none or for what does not parse. */
export function parseState(bytes: Uint8Array | undefined): unknown {
  if (!bytes || bytes.length === 0) return undefined;
  try {
    return JSON.parse(new TextDecoder().decode(bytes));
  } catch {
    return undefined;
  }
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** Every leaf of v by its path; an empty object or list is a leaf of its own. */
function leaves(v: unknown, path: string, out: Map<string, string>): void {
  if (isObject(v) && Object.keys(v).length > 0) {
    for (const [k, child] of Object.entries(v)) {
      leaves(child, path ? `${path}.${k}` : k, out);
    }
    return;
  }
  if (Array.isArray(v) && v.length > 0) {
    v.forEach((child, i) => leaves(child, `${path}[${i}]`, out));
    return;
  }
  out.set(path, JSON.stringify(v));
}

/** The fields that differ between before and after, by path. */
export function changedFields(before: unknown, after: unknown): FieldChange[] {
  const was = new Map<string, string>();
  const is = new Map<string, string>();
  leaves(before, "", was);
  leaves(after, "", is);
  const paths = [...new Set([...was.keys(), ...is.keys()])].sort();
  const out: FieldChange[] = [];
  for (const path of paths) {
    const b = was.get(path);
    const a = is.get(path);
    if (b === a) continue;
    if (b === undefined) out.push({ path, kind: "added", after: a });
    else if (a === undefined) out.push({ path, kind: "removed", before: b });
    else out.push({ path, kind: "changed", before: b, after: a });
  }
  return out;
}

/** What an entry's two states say: nothing, a resource made, one removed, or the fields changed. */
export function stateChange(
  before: Uint8Array | undefined,
  after: Uint8Array | undefined,
): StateChange {
  const b = parseState(before);
  const a = parseState(after);
  if (b === undefined && a === undefined) return { kind: "none" };
  if (b === undefined) return { kind: "created", after: a };
  if (a === undefined) return { kind: "removed", before: b };
  return { kind: "changed", fields: changedFields(b, a) };
}
