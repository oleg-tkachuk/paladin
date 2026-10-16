/**
 * The console's Content-Security-Policy. It starts strict (default-src
 * 'self') and only opens what the app needs: Next.js injects inline styles
 * and (in dev) inline/eval scripts, and the BFF talks to same-origin
 * /api/rpc so connect-src 'self' suffices. 'unsafe-eval' is dev-only.
 */

/** The page whose form must be allowed to end anywhere. */
export const OAUTH_CONSENT_PATH = "/oauth/consent";

const BASE_DIRECTIVES = [
  "default-src 'self'",
  "base-uri 'self'",
  "frame-ancestors 'none'",
  "object-src 'none'",
  "img-src 'self' data: blob:",
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
}): string {
  const directives = [
    ...BASE_DIRECTIVES,
    `script-src 'self' 'unsafe-inline'${opts.isDev ? " 'unsafe-eval'" : ""}`,
    "connect-src 'self'",
    ...(opts.consent ? [] : [FORM_ACTION_SELF]),
  ];
  return directives
    .join("; ")
    .concat(opts.isDev ? "" : "; upgrade-insecure-requests");
}
