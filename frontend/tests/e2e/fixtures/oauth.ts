/**
 * The OAuth grant, driven over HTTP the way a client drives it (ADR-0035,
 * ADR-0036): consent as the seeded admin, read the code off the 302, and
 * exchange it. The consent page only posts this same form.
 */
import { expect, type APIRequestContext } from "@playwright/test";

import { SEEDED_ADMIN } from "./credentials";
import { IAM_URL, seedAdminTenantID } from "./seed";

// The client backend/configs/compose.yaml seeds (and the dev cluster's values).
export const CLIENT_ID = "paladin-e2e-agent";
const REDIRECT_URI = "https://e2e-agent.invalid/callback";
export const SCOPE_READ = "paladin.read";
export const SCOPE_WRITE = "paladin.write";
export const AUDIENCE_ADMIN = "paladin-admin";
export const AUDIENCE_MCP = "paladin-mcp";
// The MCP server's own OAuth client (compose and the dev cluster seed it), the
// `act` of every token it exchanges and so the audit log's relayed_by.
export const MCP_SERVER_CLIENT_ID = "paladin-mcp-server";

// RFC 7636 allows 43–128 characters; the verifier only has to match itself.
const PKCE_VERIFIER = "e2e-verifier-".padEnd(64, "x");

export const HTTP_FOUND = 302;
export const HTTP_OK = 200;
export const HTTP_BAD_REQUEST = 400;
export const HTTP_UNAUTHORIZED = 401;

export interface TokenResponse {
  access_token: string;
  refresh_token: string;
  scope?: string;
}

async function pkceChallenge(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(verifier),
  );
  return Buffer.from(digest).toString("base64url");
}

/** The claims of a JWT, unverified: the planes verify it. */
export function claims(token: string): Record<string, unknown> {
  return JSON.parse(
    Buffer.from(token.split(".")[1], "base64url").toString("utf8"),
  );
}

/** Consent as the seeded admin for resource, and exchange the code. */
export async function grant(
  request: APIRequestContext,
  scopes: string[],
  resource: string = AUDIENCE_ADMIN,
): Promise<TokenResponse> {
  const authorize = await request.post(`${IAM_URL}/oauth/authorize`, {
    form: {
      client_id: CLIENT_ID,
      redirect_uri: REDIRECT_URI,
      scope: scopes.join(" "),
      resource,
      code_challenge: await pkceChallenge(PKCE_VERIFIER),
      code_challenge_method: "S256",
      state: "e2e",
      action: "allow",
      username: SEEDED_ADMIN.subject,
      password: SEEDED_ADMIN.password,
      tenant: await seedAdminTenantID(),
    },
    maxRedirects: 0,
  });
  expect(authorize.status(), await authorize.text()).toBe(HTTP_FOUND);
  const code = new URL(authorize.headers()["location"]).searchParams.get(
    "code",
  );
  expect(code, "the redirect carries a code").toBeTruthy();

  const token = await request.post(`${IAM_URL}/oauth/token`, {
    form: {
      grant_type: "authorization_code",
      code: code as string,
      client_id: CLIENT_ID,
      redirect_uri: REDIRECT_URI,
      code_verifier: PKCE_VERIFIER,
    },
  });
  expect(token.status(), await token.text()).toBe(HTTP_OK);
  return (await token.json()) as TokenResponse;
}
