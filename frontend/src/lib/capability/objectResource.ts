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

/** One object resource, by its parts. An empty key is the whole collection. */
export interface ObjectResourceParts {
  tenant: string;
  collection: string;
  key: string;
}

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

/** The collection whose segment is segment, or null when none occupies it. */
export function parseCollectionSegment(segment: string): string | null {
  if (segment.includes(RESOURCE_SEPARATOR)) return null;
  const collection = segment.replaceAll(
    COLLECTION_SEGMENT_SEPARATOR,
    COLLECTION_SEPARATOR,
  );
  return isCollectionName(collection) ? collection : null;
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
 * The parts of an object resource, or null when uri is none: the inverse of
 * objectResource. Unlike the server's parser, which reads the object a
 * resource names, an empty key is accepted, since a grant on the whole
 * collection carries one.
 */
export function parseObjectResource(uri: string): ObjectResourceParts | null {
  if (!uri.startsWith(OBJECT_RESOURCE_SCHEME)) return null;
  const rest = uri.slice(OBJECT_RESOURCE_SCHEME.length);
  const tenantEnd = rest.indexOf(RESOURCE_SEPARATOR);
  if (tenantEnd < 0) return null;
  const tenant = rest.slice(0, tenantEnd);
  const afterTenant = rest.slice(tenantEnd + RESOURCE_SEPARATOR.length);
  const segmentEnd = afterTenant.indexOf(RESOURCE_SEPARATOR);
  if (segmentEnd < 0) return null;
  const collection = parseCollectionSegment(afterTenant.slice(0, segmentEnd));
  if (collection === null || !isUuid(tenant)) return null;
  const key = afterTenant.slice(segmentEnd + RESOURCE_SEPARATOR.length);
  return { tenant, collection, key };
}

/** How far a stored resource prefix reaches, as the shared table names it. */
export const GRANT_REACH = {
  /** Every object of every tenant: the prefix restricts nothing. */
  allTenants: "all_tenants",
  /** Every object of the capability's tenant. */
  tenant: "tenant",
  /** Another tenant's objects, so nothing of the capability's tenant. */
  otherTenant: "other_tenant",
  /** One whole collection. */
  collection: "collection",
  /** The keys under a prefix ending in "/". */
  keys: "keys",
  /** One key and the keys under it. */
  key: "key",
  /** No object the server can store. */
  nothing: "nothing",
  /** No object resource: the console does not say what it reaches. */
  unknown: "unknown",
} as const;

export type GrantReach = (typeof GRANT_REACH)[keyof typeof GRANT_REACH];

/** What a stored resource prefix reaches, and the sentence that says so. */
export interface ObjectGrantReach {
  reach: GrantReach;
  text: string;
}

const reachesNothing = (why: string): ObjectGrantReach => ({
  reach: GRANT_REACH.nothing,
  text: `${why}: it reaches nothing.`,
});

const wholeCollection = (collection: string): ObjectGrantReach => ({
  reach: GRANT_REACH.collection,
  text: `The whole collection ${collection} and every object in it.`,
});

const wholeTenant: ObjectGrantReach = {
  reach: GRANT_REACH.tenant,
  text: "Every object in every collection of this tenant.",
};

/**
 * What a capability's stored resource prefix reaches in tenant (its id), as
 * limes.MatchResource matches it against the object resources the server
 * checks: a text prefix that ends at a "/" boundary. A prefix cut short still
 * reaches what it is a prefix of ("object://{tenant}" is the whole tenant,
 * "object://{tenant}/docs" the whole collection). A prefix only reaches
 * nothing when no resource the server writes can match it: a tenant id not
 * in lower case, a segment no collection is written as, or a key part the
 * key rule refuses. A prefix outside the object:// scheme is no object
 * resource, and the console does not guess at it. The tenant names the
 * capability's tenant; the tenant itself is bound elsewhere, so "every
 * tenant" says what the prefix matches, not what the capability can reach.
 */
export function describeObjectGrant(
  prefix: string,
  tenant: string,
): ObjectGrantReach {
  // A part of the scheme that ends at a "/" boundary of it reaches every
  // object resource of every tenant. Any other part reaches no object, but
  // what else it reaches is not the console's to say.
  if (
    prefix !== "" &&
    OBJECT_RESOURCE_SCHEME.startsWith(prefix) &&
    (prefix.endsWith(RESOURCE_SEPARATOR) ||
      OBJECT_RESOURCE_SCHEME[prefix.length] === RESOURCE_SEPARATOR)
  ) {
    return {
      reach: GRANT_REACH.allTenants,
      text: "Every object of every tenant, as far as this prefix goes: it restricts nothing.",
    };
  }
  if (!prefix.startsWith(OBJECT_RESOURCE_SCHEME)) {
    return {
      reach: GRANT_REACH.unknown,
      text: "Not an object resource: the console cannot say what it reaches.",
    };
  }
  const rest = prefix.slice(OBJECT_RESOURCE_SCHEME.length);
  const tenantEnd = rest.indexOf(RESOURCE_SEPARATOR);
  const named = tenantEnd < 0 ? rest : rest.slice(0, tenantEnd);
  if (!isUuid(named)) return reachesNothing(`"${named}" is no tenant id`);
  if (named !== named.toLowerCase()) {
    return reachesNothing(
      "The tenant id is not in lower case, as the server writes it",
    );
  }
  // Without the capability's tenant the panel cannot tell another tenant's
  // resource from its own, so it reads every one as its own.
  if (isUuid(tenant) && named !== tenant.toLowerCase()) {
    return {
      reach: GRANT_REACH.otherTenant,
      text: `Another tenant (${named}): nothing in this tenant.`,
    };
  }
  const afterTenant =
    tenantEnd < 0 ? "" : rest.slice(tenantEnd + RESOURCE_SEPARATOR.length);
  if (afterTenant === "") return wholeTenant;
  if (!afterTenant.includes(RESOURCE_SEPARATOR)) {
    const collection = parseCollectionSegment(afterTenant);
    return collection === null
      ? reachesNothing(`No collection is written "${afterTenant}"`)
      : wholeCollection(collection);
  }
  const parts = parseObjectResource(prefix);
  if (!parts) {
    const segment = afterTenant.slice(
      0,
      afterTenant.indexOf(RESOURCE_SEPARATOR),
    );
    return reachesNothing(`No collection is written "${segment}"`);
  }
  const { collection, key } = parts;
  if (key === "") return wholeCollection(collection);
  const refused = grantKeyProblem(key);
  if (refused) return reachesNothing(`The key part is refused (${refused})`);
  if (key.endsWith(KEY_SEPARATOR)) {
    return {
      reach: GRANT_REACH.keys,
      text: `The objects under ${key} in ${collection}.`,
    };
  }
  return {
    reach: GRANT_REACH.key,
    text: `The object ${key} in ${collection}, and the objects under ${key}${KEY_SEPARATOR}.`,
  };
}

/** Labels a prefix that names the capability's whole tenant. */
export const WHOLE_TENANT_LABEL = "whole tenant";

/** Separates the collection from the key in a short label. */
export const LABEL_KEY_SEPARATOR = " · ";

/**
 * A one-line label for a stored resource prefix, for a table cell: the
 * collection, and the key after LABEL_KEY_SEPARATOR when there is one, for a
 * prefix in tenant; WHOLE_TENANT_LABEL for the tenant itself. Any other prefix
 * — another tenant's, one cut short, one outside object:// — is returned as
 * it is stored, since a shorter form could hide what it names.
 */
export function objectGrantLabel(prefix: string, tenant: string): string {
  const own =
    OBJECT_RESOURCE_SCHEME + tenant.toLowerCase() + RESOURCE_SEPARATOR;
  if (!isUuid(tenant) || !prefix.startsWith(own)) return prefix;
  if (prefix === own) return WHOLE_TENANT_LABEL;
  const parts = parseObjectResource(prefix);
  if (!parts) return prefix;
  return parts.key === ""
    ? parts.collection
    : parts.collection + LABEL_KEY_SEPARATOR + parts.key;
}

/** The characters a resource label may wrap after: path, id and label joins. */
const LABEL_BREAK_AFTER = /(?<=[/.\-·])/u;

/**
 * label cut into the pieces a narrow column may wrap between: each ends at a
 * "/", ".", "-" or "·", so a line breaks between path segments or id groups
 * and never inside them. Joined, the pieces are label again.
 */
export function labelBreakPieces(label: string): string[] {
  return label === "" ? [] : label.split(LABEL_BREAK_AFTER);
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
