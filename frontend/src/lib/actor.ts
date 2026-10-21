import { isUuid } from "@/lib/resources/tenant-resolve";

/**
 * What an audit entry's actor_subject begins with when the actor is not a
 * person; the server writes them before the token's or the capability's id.
 * sdk/testdata/actor_subjects.json holds the server and the console to them.
 */
export const SUBJECT_PREFIX = {
  apiToken: "apikey:",
  capability: "capability:",
  system: "system:",
} as const;

/** Who an actor subject names, and the id to look them up by. */
export type Actor =
  | { kind: "user"; id: string }
  | { kind: "apiToken"; id: string }
  | { kind: "capability"; id: string }
  | { kind: "other" };

/**
 * The actor a subject names: a person by their user id, an API token or a
 * capability by the id after its prefix. Anything else — "system:bootstrap",
 * a prefix with no id after it — is shown as recorded.
 */
export function parseActor(subject: string): Actor {
  if (isUuid(subject)) return { kind: "user", id: subject };
  for (const kind of ["apiToken", "capability"] as const) {
    const prefix = SUBJECT_PREFIX[kind];
    if (subject.startsWith(prefix)) {
      const id = subject.slice(prefix.length);
      if (isUuid(id)) return { kind, id };
    }
  }
  return { kind: "other" };
}
