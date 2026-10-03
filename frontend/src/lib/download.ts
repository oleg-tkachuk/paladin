/**
 * The Content-Disposition a download URL is signed with: attachment, so the
 * browser saves the object instead of rendering it from the store's origin,
 * under the last segment of its key. The name goes in the RFC 5987 form, so
 * any character survives; the server re-encodes it before signing.
 */
export function attachmentDisposition(key: string): string {
  const base = key.split("/").filter(Boolean).pop();
  if (!base) return "attachment";
  return `attachment; filename*=UTF-8''${encodeRfc5987(base)}`;
}

/** encodeURIComponent, plus the characters it leaves that RFC 5987's
 *  attr-char does not allow. */
function encodeRfc5987(value: string): string {
  return encodeURIComponent(value).replace(
    /['()*!]/g,
    (c) => `%${c.charCodeAt(0).toString(16).toUpperCase()}`,
  );
}
