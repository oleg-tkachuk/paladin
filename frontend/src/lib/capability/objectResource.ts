/**
 * The resource a capability names objects by,
 * object://{tenant}/{collection segment}/{key or key prefix}, built and read
 * exactly as the server (objecth.CapabilityObjectURI), the Go SDK
 * (paladin.ObjectResource) and the Python SDK (paladin.object_resource) do:
 * sdk/testdata/object_resources.json holds all of them and this helper to the
 * same rows. It is the only resource kind the server asserts a capability
 * against, so every console form that grants on resources builds them here.
 *
 * A capability's resource prefix covers a resource at a "/" boundary
 * (limes.MatchResource): "…/docs/reports/" covers every key under reports/,
 * "…/docs/reports" covers the key reports and the keys under reports/, and
 * "…/docs/" the whole collection.
 */
import { isUuid } from "@/lib/resources/tenant-resolve";
import { KEY_SEPARATOR, objectKeyProblem } from "@/lib/objects/objectKey";
import { hasDotSegment } from "./resourceMatch";

/** Starts every object resource (paladin.ObjectResourceScheme). */
export const OBJECT_RESOURCE_SCHEME = "object://";

/** Separates the tenant, the collection segment and the key. */
export const RESOURCE_SEPARATOR = "/";

/**
 * Joins a nested collection's names inside the one segment the collection
 * occupies (storagepath.CollectionSegmentSeparator): a collection name holds
 * only [a-z0-9-] between its "/", so the encoding is reversible.
 */
export const COLLECTION_SEGMENT_SEPARATOR = ".";

/** The separator between a nested collection's names. */
export const COLLECTION_SEPARATOR = "/";

/** The longest collection name (storagepath.maxCollectionLength). */
export const MAX_COLLECTION_LENGTH = 255;

/** The collection grammar (storagepath.collectionGrammar). */
const COLLECTION_GRAMMAR =
  /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\/[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/;

/** Whether collection is a name the server gives a collection. */
export function isCollectionName(collection: string): boolean {
  return (
    collection.length <= MAX_COLLECTION_LENGTH &&
    COLLECTION_GRAMMAR.test(collection)
  );
}

/** The one URI segment collection occupies (storagepath.CollectionSegment). */
export function collectionSegment(collection: string): string {
  return collection.replaceAll(
    COLLECTION_SEPARATOR,
    COLLECTION_SEGMENT_SEPARATOR,
  );
}

/**
 * The resource a capability names for the object key in collection of
 * tenant, or for every object under key when it is a prefix. The server
 * writes the tenant id in lower case, so this does too: a resource in any
 * other case matches nothing.
 */
export function objectResource(
  tenant: string,
  collection: string,
  key: string,
): string {
  return (
    OBJECT_RESOURCE_SCHEME +
    tenant.toLowerCase() +
    RESOURCE_SEPARATOR +
    collectionSegment(collection) +
    RESOURCE_SEPARATOR +
    key
  );
}

/**
 * Why key may not end a grant's resource, or null. Empty is the whole
 * collection. Otherwise it names an object key, or every key under it when it
 * ends with "/", and is held to the key rule without that last "/": a grant
 * on a key no object can have covers nothing. A segment limes reads as "."
 * or ".." (percent-encoded, say) is refused too: the server refuses the
 * resource at issue. The tenant id and the collection segment can hold no
 * such segment, so the key decides it for the whole resource.
 */
export function grantKeyProblem(key: string): string | null {
  if (key === "") return null;
  const named =
    key.length > KEY_SEPARATOR.length && key.endsWith(KEY_SEPARATOR)
      ? key.slice(0, -KEY_SEPARATOR.length)
      : key;
  const problem = objectKeyProblem(named);
  if (problem) return problem;
  return hasDotSegment(key) ? DOT_SEGMENT_REASON : null;
}

/** Why a key holding a segment limes reads as a dot segment is refused. */
export const DOT_SEGMENT_REASON =
  'it holds a segment the server reads as "." or ".."';

/** A grant the console builds: a collection, and a key or key prefix. */
export interface ObjectGrantDraft {
  collection: string;
  key: string;
}

/** Why draft names no grantable resource, or null. */
export function objectGrantProblem(draft: ObjectGrantDraft): string | null {
  if (draft.collection === "") return "Choose a collection.";
  if (!isCollectionName(draft.collection)) {
    return `"${draft.collection}" is not a collection name.`;
  }
  const key = grantKeyProblem(draft.key);
  return key ? `The key prefix is refused: ${key}.` : null;
}

/** The reason a row repeats a resource an earlier row names. */
export const DUPLICATE_GRANT = "This resource is already listed.";

/** The reason no row can be built before the tenant's id is known. */
export const TENANT_UNKNOWN = "The tenant has no id yet.";

/**
 * Why each of drafts cannot be granted on tenant, row for row, null where it
 * can: a row repeating an earlier row's resource is refused too.
 */
export function objectGrantProblems(
  tenant: string,
  drafts: ObjectGrantDraft[],
): (string | null)[] {
  const tenantKnown = isUuid(tenant);
  const seen = new Set<string>();
  return drafts.map((draft) => {
    if (!tenantKnown) return TENANT_UNKNOWN;
    const reason = objectGrantProblem(draft);
    if (reason) return reason;
    const uri = objectResource(tenant, draft.collection, draft.key);
    if (seen.has(uri)) return DUPLICATE_GRANT;
    seen.add(uri);
    return null;
  });
}

/**
 * The first refused row of drafts, by its 1-based number, and why; null when
 * every row can be granted.
 */
export function firstObjectGrantProblem(
  tenant: string,
  drafts: ObjectGrantDraft[],
): { row: number; reason: string } | null {
  const problems = objectGrantProblems(tenant, drafts);
  const i = problems.findIndex((p) => p !== null);
  return i < 0 ? null : { row: i + 1, reason: problems[i]! };
}
