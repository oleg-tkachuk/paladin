// Which objects the console can show in place, and how. Pure, so the
// inspector and the object page decide alike and the rules are tested once.
//
// Images render in an <img>, which runs no script, SVG included. Text is read
// with fetch and shown as text, never as markup, so an HTML object shows its
// source. A PDF opens in a frame on its own presigned URL, signed inline. The
// store's CORS answer lists no Range header, so a text object is read whole,
// and only when it is small.

/** How an object is shown in place. */
export type PreviewKind = "image" | "text" | "pdf";

const BYTES_PER_KIB = 1024;
/** The largest text object read for a preview, whole. */
export const TEXT_PREVIEW_MAX_BYTES = 256 * BYTES_PER_KIB;

const IMAGE_PREFIX = "image/";
const TEXT_PREFIX = "text/";
const PDF_TYPE = "application/pdf";
/** Structured text that is not under text/. */
const TEXT_TYPES = new Set([
  "application/json",
  "application/xml",
  "application/yaml",
  "application/x-yaml",
  "application/toml",
  "application/x-ndjson",
]);
/** Images browsers do not decode, which would show as a broken picture. */
const UNRENDERED_IMAGES = new Set(["image/heic", "image/heif", "image/tiff"]);

/** The media type without its parameters, in lower case. */
export function mediaType(contentType: string): string {
  return contentType.split(";")[0].trim().toLowerCase();
}

/** How an object can be shown, or null when it cannot. */
export function previewKind(
  contentType: string,
  sizeBytes: bigint,
): PreviewKind | null {
  const type = mediaType(contentType);
  if (type.startsWith(IMAGE_PREFIX)) {
    return UNRENDERED_IMAGES.has(type) ? null : "image";
  }
  if (type === PDF_TYPE) return "pdf";
  if (type.startsWith(TEXT_PREFIX) || TEXT_TYPES.has(type)) {
    return sizeBytes <= BigInt(TEXT_PREVIEW_MAX_BYTES) ? "text" : null;
  }
  return null;
}
