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
export const REDIRECT_URI = "https://e2e-agent.invalid/callback";
export const SCOPE_READ = "paladin.read";
export const SCOPE_WRITE = "paladin.write";
export const AUDIENCE_ADMIN = "paladin-admin";
export const AUDIENCE_DATA = "paladin-data";
export const AUDIENCE_MCP = "paladin-mcp";
// The MCP server's own OAuth client (compose and the dev cluster seed it), the
// `act` of every token it exchanges and so the audit log's relayed_by.
export const MCP_SERVER_CLIENT_ID = "paladin-mcp-server";

// RFC 7636 allows 43–128 characters; the verifier only has to match itself.
export const PKCE_VERIFIER = "e2e-verifier-".padEnd(64, "x");

export const HTTP_CREATED = 201;
export const HTTP_FOUND = 302;
export const HTTP_OK = 200;
export const HTTP_BAD_REQUEST = 400;
export const HTTP_UNAUTHORIZED = 401;

// The RFC 7009 endpoint a client gives a grant up at.
const REVOKE_PATH = "/oauth/revoke";

/**
 * Every grant this worker's specs were issued, by one of its refresh tokens
 * and the client it went to. Each grant used to outlive its test: the seeded
 * admin's connected applications held a row per grant per run, and the revoke
 * spec waded through all of them.
 */
const issued: { refreshToken: string; clientId: string }[] = [];

/**
 * Give up every grant issued since the last call, as the client it went to.
 * Any of a grant's refresh tokens ends it, rotated ones included. A client its
 * test deleted answers invalid_client: its grants went with it.
 */
export async function revokeIssuedGrants(
  request: APIRequestContext,
): Promise<void> {
  for (const g of issued.splice(0)) {
    const res = await request.post(`${IAM_URL}${REVOKE_PATH}`, {
      form: { client_id: g.clientId, token: g.refreshToken },
    });
    expect(
      [HTTP_OK, HTTP_UNAUTHORIZED],
      `revoking a grant of ${g.clientId}: ${await res.text()}`,
    ).toContain(res.status());
  }
}

export interface TokenResponse {
  access_token: string;
  refresh_token: string;
  scope?: string;
}

export async function pkceChallenge(verifier: string): Promise<string> {
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

/**
 * Register a public client the way an agent does (RFC 7591), for REDIRECT_URI
 * and audiences, and return its client_id. The stack runs with
 * auth.oauth.dynamic_registration on.
 */
export async function registerClient(
  request: APIRequestContext,
  clientName: string,
  audiences: string[],
): Promise<string> {
  const res = await request.post(`${IAM_URL}/oauth/register`, {
    data: {
      client_name: clientName,
      redirect_uris: [REDIRECT_URI],
      audiences,
      token_endpoint_auth_method: "none",
    },
  });
  expect(res.status(), await res.text()).toBe(HTTP_CREATED);
  return ((await res.json()) as { client_id: string }).client_id;
}

/** Consent as the seeded admin for resource, and exchange the code. */
export async function grant(
  request: APIRequestContext,
  scopes: string[],
  resource: string = AUDIENCE_ADMIN,
  clientId: string = CLIENT_ID,
): Promise<TokenResponse> {
  const authorize = await request.post(`${IAM_URL}/oauth/authorize`, {
    form: {
      client_id: clientId,
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

  return exchangeCode(request, code as string, clientId);
}

/** Exchange an authorization code issued for PKCE_VERIFIER. */
export async function exchangeCode(
  request: APIRequestContext,
  code: string,
  clientId: string = CLIENT_ID,
): Promise<TokenResponse> {
  const token = await request.post(`${IAM_URL}/oauth/token`, {
    form: {
      grant_type: "authorization_code",
      code,
      client_id: clientId,
      redirect_uri: REDIRECT_URI,
      code_verifier: PKCE_VERIFIER,
    },
  });
  expect(token.status(), await token.text()).toBe(HTTP_OK);
  const body = (await token.json()) as TokenResponse;
  issued.push({ refreshToken: body.refresh_token, clientId });
  return body;
}
