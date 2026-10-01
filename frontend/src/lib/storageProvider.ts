// Vendor/implementation behind a backend's `kind` (`kind` is too coarse —
// every self-hosted S3 is S3_COMPATIBLE). The server carries an explicit
// `provider` slug (mirrored from config); when unset the endpoint's host is
// used to guess one, so the "Type" column is still useful for backends
// predating the field. Known slugs get a label; unknown ones render verbatim.

export const PROVIDER_LABELS: Record<string, string> = {
  garage: "Garage",
  seaweedfs: "SeaweedFS",
  minio: "MinIO",
  ceph: "Ceph",
  aws: "AWS",
  gcp: "GCP",
  azure: "Azure",
  digitalocean: "DigitalOcean",
  wasabi: "Wasabi",
  backblaze: "Backblaze",
  cloudflare: "Cloudflare R2",
};

/** Hosted providers, matched on the host's registered domain. */
const PROVIDER_DOMAINS: readonly (readonly [string, string])[] = [
  ["amazonaws.com", "aws"],
  ["googleapis.com", "gcp"],
  ["digitaloceanspaces.com", "digitalocean"],
  ["r2.cloudflarestorage.com", "cloudflare"],
  ["wasabisys.com", "wasabi"],
  ["backblazeb2.com", "backblaze"],
  ["blob.core.windows.net", "azure"],
];

/** Self-hosted implementations, recognised by a name in the host. */
const PROVIDER_HOST_NAMES: readonly (readonly [string, string])[] = [
  ["garage", "garage"],
  ["seaweed", "seaweedfs"],
  ["minio", "minio"],
];

/** Scheme assumed for an endpoint written without one, so it parses. */
const DEFAULT_SCHEME = "http://";

function endpointHost(endpoint: string): string {
  const e = endpoint.trim();
  if (!e) return "";
  try {
    return new URL(e.includes("://") ? e : DEFAULT_SCHEME + e).hostname;
  } catch {
    return "";
  }
}

/** The provider slug the endpoint's host suggests, or "" when none does. */
export function providerFromEndpoint(endpoint: string): string {
  const host = endpointHost(endpoint).toLowerCase();
  if (!host) return "";
  for (const [domain, slug] of PROVIDER_DOMAINS) {
    if (host === domain || host.endsWith("." + domain)) return slug;
  }
  for (const [name, slug] of PROVIDER_HOST_NAMES) {
    if (host.includes(name)) return slug;
  }
  return "";
}

/**
 * The display label: an explicit `provider` wins, else the endpoint guess,
 * else "—". `derived` flags a guess so the UI can mark it unconfirmed.
 */
export function providerLabel(b: { provider: string; endpoint: string }): {
  label: string;
  derived: boolean;
} {
  const slug = b.provider || providerFromEndpoint(b.endpoint);
  if (!slug) return { label: "—", derived: false };
  return { label: PROVIDER_LABELS[slug] || slug, derived: !b.provider };
}
