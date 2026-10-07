// What a storage backend's feature probe found (ADR-0026), turned into what
// an operator reads: a label per feature, and the warnings a backend's
// results add up to. The server sends every catalog feature, with whether
// Paladin requires it and what it enables, so nothing here restates the
// catalog beyond a display name.

import {
  FeatureSupport,
  StorageCompatibility,
  StorageFeature,
  type StorageBackend,
  type StorageFeatureSupport,
} from "@/gen/paladin/admin/v1/types_pb";

export const FEATURE_LABELS: Record<StorageFeature, string> = {
  [StorageFeature.UNSPECIFIED]: "—",
  [StorageFeature.CONDITIONAL_PUT]: "Conditional PUT",
  [StorageFeature.CHECKSUM_SHA256]: "SHA-256 checksums",
  [StorageFeature.MULTIPART_UPLOAD]: "Multipart upload",
  [StorageFeature.SERVER_SIDE_COPY]: "Server-side copy",
  [StorageFeature.PRESIGNED_POST]: "Form upload (POST)",
  [StorageFeature.BUCKET_CREATE]: "Bucket creation",
  [StorageFeature.ANONYMOUS_READ_POLICY]: "Anonymous read policy",
};

export const SUPPORT_LABELS: Record<FeatureSupport, string> = {
  [FeatureSupport.UNSPECIFIED]: "—",
  [FeatureSupport.SUPPORTED]: "Supported",
  [FeatureSupport.UNSUPPORTED]: "Unsupported",
  [FeatureSupport.UNKNOWN]: "Unknown",
};

export type WarningLevel = "error" | "warning" | "info";

export interface FeatureWarning {
  level: WarningLevel;
  text: string;
}

const neverProbed = (f: StorageFeatureSupport) => f.checkedAt === undefined;

/**
 * The warnings a backend's probe results add up to, most severe first: a
 * required feature unsupported breaks Paladin's guarantees on the backend; an
 * optional one unsupported disables what it enables; a backend never probed
 * has shown nothing yet.
 */
export function featureWarnings(
  backend: Pick<StorageBackend, "features" | "compatibility">,
): FeatureWarning[] {
  const out: FeatureWarning[] = [];
  const unsupported = backend.features.filter(
    (f) => f.support === FeatureSupport.UNSUPPORTED,
  );
  for (const f of unsupported.filter((f) => f.required)) {
    out.push({
      level: "error",
      text: `${FEATURE_LABELS[f.feature]} is unsupported. Paladin relies on it for ${f.enables}, so this backend is incompatible.`,
    });
  }
  for (const f of unsupported.filter((f) => !f.required)) {
    out.push({
      level: "warning",
      text: `${FEATURE_LABELS[f.feature]} is unsupported. Not available on this backend: ${f.enables}.`,
    });
  }
  if (backend.features.every(neverProbed)) {
    out.push({
      level: "info",
      text: "Not probed yet: run Test connectivity to find which S3 features this backend supports.",
    });
  } else if (backend.compatibility === StorageCompatibility.UNVERIFIED) {
    out.push({
      level: "info",
      text: "Some required features could not be probed; see the table for why.",
    });
  }
  return out;
}

/** The one-word compatibility a backend list row shows. */
export function compatibilityBadge(
  backend: Pick<StorageBackend, "features" | "compatibility">,
): {
  label: string;
  variant: "destructive" | "warning" | "secondary" | "success";
} {
  if (backend.compatibility === StorageCompatibility.INCOMPATIBLE) {
    return { label: "Incompatible", variant: "destructive" };
  }
  const limited = backend.features.some(
    (f) => !f.required && f.support === FeatureSupport.UNSUPPORTED,
  );
  if (backend.compatibility === StorageCompatibility.COMPATIBLE) {
    return limited
      ? { label: "Limited", variant: "warning" }
      : { label: "Compatible", variant: "success" };
  }
  return { label: "Unverified", variant: "secondary" };
}

/** Whether the backend's last probe found the feature supported. */
export function supports(
  backend: Pick<StorageBackend, "features">,
  feature: StorageFeature,
): boolean {
  return backend.features.some(
    (f) => f.feature === feature && f.support === FeatureSupport.SUPPORTED,
  );
}
