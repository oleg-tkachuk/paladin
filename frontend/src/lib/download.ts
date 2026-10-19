/**
 * The Content-Disposition a download URL is signed with: attachment, so the
 * browser saves the object instead of rendering it from the store's origin,
 * under the last segment of its key. The name goes in the RFC 5987 form, so
 * any character survives; the server re-encodes it before signing.
 */
export function attachmentDisposition(key: string): string {
  return disposition(ATTACHMENT, key);
}

/**
 * The Content-Disposition a preview URL is signed with: inline, so a frame
 * shows the object rather than saving it. Only for kinds the browser renders
 * without running the object as a page of the store's origin (a PDF).
 */
export function inlineDisposition(key: string): string {
  return disposition(INLINE, key);
}

const ATTACHMENT = "attachment";
const INLINE = "inline";

function disposition(type: string, key: string): string {
  const base = key.split("/").filter(Boolean).pop();
  if (!base) return type;
  return `${type}; filename*=UTF-8''${encodeRfc5987(base)}`;
}

/** encodeURIComponent, plus the characters it leaves that RFC 5987's
 *  attr-char does not allow. */
function encodeRfc5987(value: string): string {
  return encodeURIComponent(value).replace(
    /['()*!]/g,
    (c) => `%${c.charCodeAt(0).toString(16).toUpperCase()}`,
  );
}
