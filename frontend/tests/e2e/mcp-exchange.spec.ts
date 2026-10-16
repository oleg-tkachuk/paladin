/**
 * The MCP server is its own audience (ADR-0036), end to end.
 *
 * An agent authorizes for the MCP resource URL and is issued paladin-mcp,
 * which no plane accepts; the MCP server takes nothing else, and exchanges
 * it for each plane a tool calls, so one session reaches them all. The IAM
 * plane is not called: no profile offers an IAM tool.
 *
 * MCP is spoken by hand over streamable HTTP, one JSON-RPC message a POST.
 */
import { test, expect, type APIRequestContext } from "@playwright/test";

import {
  AUDIENCE_ADMIN,
  AUDIENCE_MCP,
  CLIENT_ID,
  HTTP_UNAUTHORIZED,
  SCOPE_READ,
  claims,
  grant,
} from "./fixtures/oauth";
import {
  ADMIN_URL,
  MCP_RESOURCE,
  MCP_URL,
  seedAdminTenantID,
} from "./fixtures/seed";

const READ_RPC = "/paladin.admin.v1.BackendService/ListBackends";
// The protocol version offered; the server answers with the one it speaks.
const OFFERED_PROTOCOL = "2025-06-18";
const SESSION_HEADER = "mcp-session-id";
const PROTOCOL_HEADER = "mcp-protocol-version";
// What the bridge says when a plane refuses the token it sent
// (backend/internal/mcp/errors.go).
const TOKEN_REFUSED = "access token was refused";
// A collection no fixture creates: the data plane answers for it without the
// token being in question.
const MISSING_COLLECTION = "e2e-mcp-exchange-missing";
// How the bridge renders a plane's answer about the request itself:
// "<connect code>: <message>" (backend/internal/mcp/errors.go).
const PLANE_ANSWER = /^(not_found|permission_denied): /;

interface RPCResponse {
  result?: {
    protocolVersion?: string;
    isError?: boolean;
    content?: { type: string; text?: string }[];
  };
  error?: { code: number; message: string };
}

/** The JSON-RPC response in a streamable-HTTP reply, JSON or SSE. */
async function rpcBody(
  text: string,
  contentType: string,
): Promise<RPCResponse> {
  if (!contentType.includes("text/event-stream")) {
    return JSON.parse(text) as RPCResponse;
  }
  const data = text
    .split("\n")
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice("data:".length).trim())
    .filter(Boolean);
  return JSON.parse(data[data.length - 1]) as RPCResponse;
}

class MCPSession {
  private id = 0;
  private sessionID = "";
  private protocol = OFFERED_PROTOCOL;

  constructor(
    private readonly request: APIRequestContext,
    private readonly token: string,
  ) {}

  private headers(): Record<string, string> {
    const h: Record<string, string> = {
      Authorization: `Bearer ${this.token}`,
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
    };
    if (this.sessionID) {
      h[SESSION_HEADER] = this.sessionID;
      h[PROTOCOL_HEADER] = this.protocol;
    }
    return h;
  }

  async post(method: string, params: object, notify = false) {
    const body = notify
      ? { jsonrpc: "2.0", method, params }
      : { jsonrpc: "2.0", id: ++this.id, method, params };
    return this.request.post(MCP_URL, { headers: this.headers(), data: body });
  }

  async initialize(): Promise<void> {
    const res = await this.post("initialize", {
      protocolVersion: OFFERED_PROTOCOL,
      capabilities: {},
      clientInfo: { name: "paladin-e2e", version: "1" },
    });
    expect(res.ok(), await res.text()).toBe(true);
    this.sessionID = res.headers()[SESSION_HEADER] ?? "";
    const body = await rpcBody(
      await res.text(),
      res.headers()["content-type"] ?? "",
    );
    this.protocol = body.result?.protocolVersion ?? OFFERED_PROTOCOL;
    const ack = await this.post("notifications/initialized", {}, true);
    expect(ack.ok(), await ack.text()).toBe(true);
  }

  /** A tool's result text, and whether the tool reported an error. */
  async callTool(
    name: string,
    args: object,
  ): Promise<{ isError: boolean; text: string }> {
    const res = await this.post("tools/call", { name, arguments: args });
    expect(res.ok(), await res.text()).toBe(true);
    const body = await rpcBody(
      await res.text(),
      res.headers()["content-type"] ?? "",
    );
    expect(body.error, JSON.stringify(body.error)).toBeUndefined();
    const text = (body.result?.content ?? [])
      .map((c) => c.text ?? "")
      .join("\n");
    return { isError: body.result?.isError ?? false, text };
  }
}

test.describe("MCP token exchange", () => {
  test("the MCP resource URL is issued paladin-mcp, which no plane accepts", async ({
    request,
  }) => {
    const tok = await grant(request, [SCOPE_READ], MCP_RESOURCE);
    expect(claims(tok.access_token)).toMatchObject({
      aud: AUDIENCE_MCP,
      client_id: CLIENT_ID,
      scope: SCOPE_READ,
    });
    const res = await request.post(`${ADMIN_URL}${READ_RPC}`, {
      headers: {
        Authorization: `Bearer ${tok.access_token}`,
        "Content-Type": "application/json",
      },
      data: {},
    });
    expect(((await res.json()) as { code: string }).code).toBe(
      "unauthenticated",
    );
  });

  test("a plane token does not open an MCP session", async ({ request }) => {
    const plane = await grant(request, [SCOPE_READ], AUDIENCE_ADMIN);
    const res = await new MCPSession(request, plane.access_token).post(
      "initialize",
      {
        protocolVersion: OFFERED_PROTOCOL,
        capabilities: {},
        clientInfo: { name: "paladin-e2e", version: "1" },
      },
    );
    expect(res.status()).toBe(HTTP_UNAUTHORIZED);
  });

  test("one session reaches the admin and the data plane", async ({
    request,
  }) => {
    const tok = await grant(request, [SCOPE_READ], MCP_RESOURCE);
    const session = new MCPSession(request, tok.access_token);
    await session.initialize();

    const admin = await session.callTool("paladin_list_backends", {});
    expect(admin.isError, admin.text).toBe(false);

    const data = await session.callTool("paladin_query_objects", {
      tenant_id: await seedAdminTenantID(),
      collection: MISSING_COLLECTION,
    });
    // The data plane answered about the collection — a listing, or its own
    // refusal, which the bridge renders code first. Either needs a token it
    // accepted; a refused token or a failed exchange says otherwise.
    if (data.isError) expect(data.text).toMatch(PLANE_ANSWER);
    expect(data.text).not.toContain(TOKEN_REFUSED);
  });
});
