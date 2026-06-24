import { z } from "zod";

/**
 * safeParseJson parses a JSON string and validates it against a Zod schema.
 *
 * Use it for any **non-self-authored** data — localStorage, URL params,
 * pasted JSON, decoded tokens — so a malformed or hostile payload becomes a
 * handled `null` instead of a thrown `TypeError` (bad JSON) or, worse, a
 * silently-wrong value that an `as` cast would have waved through.
 *
 * Returns the validated, typed value on success; `null` on ANY failure
 * (`raw` is null/empty, `JSON.parse` throws, or the shape doesn't match the
 * schema). Callers decide the fallback: a stored-preference reader uses
 * `?? defaultValue`; an editor surfaces a "couldn't read saved data" message.
 *
 *   const SavedSchema = z.object({ kind: z.string().optional() });
 *   const saved = safeParseJson(SavedSchema, localStorage.getItem(key)) ?? {};
 */
export function safeParseJson<T>(
  schema: z.ZodType<T>,
  raw: string | null | undefined,
): T | null {
  if (raw == null || raw === "") return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  const result = schema.safeParse(parsed);
  return result.success ? result.data : null;
}
