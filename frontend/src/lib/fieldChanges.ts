// What changed between two JSON documents, field by field: each leaf that
// was added, removed or given a new value, named by its path. An audit
// entry's snapshots are records, not text, so a list of the fields an
// action touched reads better than a line diff of their pretty-printed form,
// where a nested change is a pair of lines out of context.

export type FieldChange =
  | { kind: "added"; path: string; after: unknown }
  | { kind: "removed"; path: string; before: unknown }
  | { kind: "changed"; path: string; before: unknown; after: unknown };

const IDENTIFIER = /^[A-Za-z_$][\w$]*$/;

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** A key as it joins a path: bare when it is a name, quoted when not. */
function pathSegment(key: string): string {
  return IDENTIFIER.test(key) ? key : JSON.stringify(key);
}

function join(path: string, key: string): string {
  return path ? `${path}.${pathSegment(key)}` : pathSegment(key);
}

function equal(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/**
 * The fields that differ between `before` and `after`, in the order their
 * keys appear (before's first, then any after adds). Objects are walked; an
 * array, like a scalar, is one value, compared whole: an entry put at its
 * front would otherwise read as every later entry changed.
 */
export function fieldChanges(before: unknown, after: unknown): FieldChange[] {
  const out: FieldChange[] = [];
  const walk = (b: unknown, a: unknown, path: string) => {
    if (isRecord(b) && isRecord(a)) {
      const keys = [...Object.keys(b)];
      for (const k of Object.keys(a)) if (!(k in b)) keys.push(k);
      for (const k of keys) {
        const p = join(path, k);
        if (!(k in a)) out.push({ kind: "removed", path: p, before: b[k] });
        else if (!(k in b)) out.push({ kind: "added", path: p, after: a[k] });
        else walk(b[k], a[k], p);
      }
      return;
    }
    if (!equal(b, a)) out.push({ kind: "changed", path, before: b, after: a });
  };
  walk(before, after, "");
  return out;
}

/** A value as it reads in a change: compact JSON. */
export function fieldValue(v: unknown): string {
  return JSON.stringify(v) ?? String(v);
}
