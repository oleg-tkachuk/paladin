/**
 * Bucket resource names.
 *
 * A reference to a bucket travels as its resource name — one string,
 * "storageBackends/{backendId}/buckets/{bucketId}" — rather than as a
 * (backendId, bucketId) pair (AIP-122). The parts are still what the UI
 * shows, so the split lives here instead of being re-implemented at each
 * badge and breadcrumb.
 */

export interface BucketRef {
  backendId: string;
  bucketId: string;
}

/** Builds the resource name a request field expects. */
export function bucketResourceName(
  backendId: string,
  bucketId: string,
): string {
  return `storageBackends/${backendId}/buckets/${bucketId}`;
}

/**
 * Splits a bucket resource name into its parts, or returns null when the
 * string is not one. Callers render the raw value in that case: an
 * unparseable reference is still worth showing, and guessing at parts we
 * cannot see would be worse than displaying what the server sent.
 */
export function parseBucketResourceName(name: string): BucketRef | null {
  const m = /^storageBackends\/([^/]+)\/buckets\/([^/]+)$/.exec(name);
  if (!m) return null;
  return { backendId: m[1], bucketId: m[2] };
}
