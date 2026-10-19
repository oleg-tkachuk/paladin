/**
 * The storage origins the browser may reach directly. A presigned URL points
 * at a storage backend's public endpoint, which is another origin than the
 * console's: the upload PUTs and the image previews go there, and a policy of
 * 'self' alone blocks both. Which origins those are is the deployment's to
 * say, so they come from the environment and are read when the server starts,
 * not when the image is built.
 */

/** The variable that lists them, comma-separated: `https://s3.example.com`. */
export const STORAGE_ORIGINS_ENV = "PALADIN_STORAGE_ORIGINS";

const LIST_SEPARATOR = ",";
const ALLOWED_PROTOCOLS = new Set(["https:", "http:"]);
const WILDCARD = "*";

export interface StorageOrigins {
  /** The origins, each as `URL.origin` writes it. */
  origins: string[];
  /** The entries that are not a bare http(s) origin, left out. */
  invalid: string[];
}

/**
 * Reads the list. An entry is kept only when it is an origin and nothing
 * more: a path, a query or a wildcard would widen or blur what the policy
 * allows, so such an entry is reported and dropped rather than guessed at.
 */
export function parseStorageOrigins(raw: string | undefined): StorageOrigins {
  const origins: string[] = [];
  const invalid: string[] = [];
  for (const entry of (raw ?? "").split(LIST_SEPARATOR)) {
    const value = entry.trim();
    if (!value) continue;
    const origin = originOf(value);
    if (origin === null) invalid.push(value);
    else if (!origins.includes(origin)) origins.push(origin);
  }
  return { origins, invalid };
}

function originOf(value: string): string | null {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return null;
  }
  if (!ALLOWED_PROTOCOLS.has(url.protocol)) return null;
  // The URL parser takes `*.example.com` as a host name; to a policy it is a
  // wildcard over every subdomain.
  if (url.hostname.includes(WILDCARD)) return null;
  // `https://s3.example.com/` and `https://s3.example.com` name the same
  // origin; anything past the root does not.
  const bare = value.replace(/\/$/, "");
  return bare.toLowerCase() === url.origin ? url.origin : null;
}
