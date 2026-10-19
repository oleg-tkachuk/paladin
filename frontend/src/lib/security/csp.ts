/**
 * The console's Content-Security-Policy. It starts strict (default-src
 * 'self') and only opens what the app needs: Next.js injects inline styles
 * and (in dev) inline/eval scripts, and the BFF talks to same-origin
 * /api/rpc. The one other place the browser goes is a storage backend, on a
 * presigned URL: the deployment names those origins (storageOrigins.ts), and
 * they are added to connect-src for uploads and text previews, img-src for
 * images and frame-src for PDFs.
 * 'unsafe-eval' is dev-only.
 */

/** The response header the policy travels in. */
export const CSP_HEADER = "Content-Security-Policy";

/** The page whose form must be allowed to end anywhere. */
export const OAUTH_CONSENT_PATH = "/oauth/consent";

const BASE_DIRECTIVES = [
  "default-src 'self'",
  "base-uri 'self'",
  "frame-ancestors 'none'",
  "object-src 'none'",
  "font-src 'self' data:",
  "style-src 'self' 'unsafe-inline'",
];

// Every form posts to this origin — and on every page but one, ends there.
const FORM_ACTION_SELF = "form-action 'self'";

/**
 * The policy for a page. `consent` drops form-action: the consent form posts
 * to the console's /oauth/authorize, which answers with a redirect to the
 * client's redirect_uri — another origin, often a custom scheme like
 * desktop-agent:// — and browsers hold a form's redirects to form-action
 * too. No source list can name every client's scheme, and form-action does
 * not fall back to default-src, so leaving it out is what lets the redirect
 * through. The page renders nothing but its own form and escaped text.
 */
export function contentSecurityPolicy(opts: {
  isDev: boolean;
  consent?: boolean;
  /** Storage origins presigned URLs point at; bare origins only. */
  storageOrigins?: readonly string[];
}): string {
  const storage = (opts.storageOrigins ?? []).map((o) => ` ${o}`).join("");
  const directives = [
    ...BASE_DIRECTIVES,
    `img-src 'self' data: blob:${storage}`,
    `script-src 'self' 'unsafe-inline'${opts.isDev ? " 'unsafe-eval'" : ""}`,
    `connect-src 'self'${storage}`,
    `frame-src 'self'${storage}`,
    ...(opts.consent ? [] : [FORM_ACTION_SELF]),
  ];
  return directives
    .join("; ")
    .concat(opts.isDev ? "" : "; upgrade-insecure-requests");
}
