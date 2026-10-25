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
import { type APIRequestContext } from "@playwright/test";

import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import { createHash } from "node:crypto";
import { createClient } from "@connectrpc/connect";

import { AuditLogService } from "@/gen/paladin/admin/v1/audit_service_pb";
import { NO_DURATION } from "@/lib/format/duration";
import { tableParam } from "@/lib/table/urlState";

import {
  AUDIENCE_ADMIN,
  AUDIENCE_MCP,
  CLIENT_ID,
  HTTP_UNAUTHORIZED,
  MCP_SERVER_CLIENT_ID,
  SCOPE_READ,
  SCOPE_WRITE,
  claims,
  grant,
  revokeIssuedGrants,
} from "./fixtures/oauth";
import {
  ADMIN_URL,
  MCP_RESOURCE,
  MCP_URL,
  adminTransport,
  seedAdminTenantID,
  seedObject,
  seedPhysicalBucket,
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
// The audited RPC behind paladin_upload_object, and a one-byte body for it.
const UPLOAD_ACTION = "/paladin.data.v1.ObjectService/UploadObject";
const UPLOAD_BYTES = "x";
const UPLOAD_SHA256 = createHash("sha256")
  .update(UPLOAD_BYTES)
  .digest("base64");
// How much of a UUID names the run's collection.
const UNIQUE_SUFFIX = 8;
const AUDIT_PAGE_SIZE = 50;
// The tag the set_object_tags test writes, unique to the run.
const TAG_KEY = "e2e-mcp";
// The console page listing live sessions, and the namespace its sessions table
// keeps its view under (SESSIONS_TABLE in MCPSessions.tsx).
const MCP_PAGE = "/mcp";
const SESSIONS_PARAM = tableParam("row", { namespace: "sessions" });
// The session panel's field for how long the session has been open.
const CONNECTED_FOR = "Connected for";
// How long the session list, which polls, takes to show a new session.
const SESSION_LISTED_MS = 15_000;

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

  /** The id the server minted on initialize; empty before it. */
  get session(): string {
    return this.sessionID;
  }

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
  // Give up every grant the test was issued, so none outlives it.
  test.afterEach(async ({ request }) => {
    await revokeIssuedGrants(request);
  });

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

  test("a call through the MCP server is audited as relayed by it", async ({
    request,
  }) => {
    const tok = await grant(request, [SCOPE_READ, SCOPE_WRITE], MCP_RESOURCE);
    const session = new MCPSession(request, tok.access_token);
    await session.initialize();

    // A write the data plane audits, failed or not: an upload into a
    // collection no fixture creates, named for this run so its audit row is
    // the only one that carries the name. Nothing is reserved.
    const marker = `e2e-relay-${crypto.randomUUID().slice(0, UNIQUE_SUFFIX)}`;
    const write = await session.callTool("paladin_upload_object", {
      parent: `tenants/${await seedAdminTenantID()}/collections/${marker}`,
      content_type: "text/plain",
      size_bytes: UPLOAD_BYTES.length,
      checksum_value: UPLOAD_SHA256,
    });
    expect(write.text).not.toContain(TOKEN_REFUSED);

    const audit = createClient(AuditLogService, adminTransport());
    const { entries } = await audit.listAuditLog({
      filter: `relayed_by == "${MCP_SERVER_CLIENT_ID}" && action == "${UPLOAD_ACTION}" && search.contains("${marker}")`,
      page: { pageSize: AUDIT_PAGE_SIZE },
    });
    expect(entries.length, "the relayed write is in the audit log").toBe(1);
    expect(entries[0].relayedBy).toBe(MCP_SERVER_CLIENT_ID);
    expect(entries[0].actorAudience).toBe("paladin-data");
  });

  // A tag write through the MCP server reaches the object: the agent reads
  // the version, as the tool asks, and the plane accepts the write under it.
  // It used to send no version and be refused before the handler ran.
  test("an agent sets an object's tags under the version it read", async ({
    request,
    makeCollection,
  }) => {
    const tenantId = await seedAdminTenantID();
    const collection = await makeCollection({
      tenantId,
      bucket: await seedPhysicalBucket(),
    });
    const obj = await seedObject({
      tenantId,
      collection: collection.collection,
    });

    const tok = await grant(request, [SCOPE_READ, SCOPE_WRITE], MCP_RESOURCE);
    const session = new MCPSession(request, tok.access_token);
    await session.initialize();

    const read = await session.callTool("paladin_get_object", {
      name: obj.name,
    });
    expect(read.isError, read.text).toBe(false);
    const version = (JSON.parse(read.text) as { resource_version: string })
      .resource_version;
    expect(version, read.text).toBeTruthy();

    const marker = crypto.randomUUID();
    const write = await session.callTool("paladin_set_object_tags", {
      name: obj.name,
      resource_version: version,
      tags: { [TAG_KEY]: marker },
    });
    expect(write.isError, write.text).toBe(false);

    const after = await session.callTool("paladin_get_object", {
      name: obj.name,
    });
    const tags = (JSON.parse(after.text) as { tags?: Record<string, string> })
      .tags;
    expect(tags?.[TAG_KEY]).toBe(marker);
  });

  // The console lists the session the agent just opened, and its record opens
  // in the detail panel, linked from the URL, naming the agent by the token's
  // subject.
  test("the console opens a live session's record", async ({
    page,
    request,
  }) => {
    const tok = await grant(request, [SCOPE_READ], MCP_RESOURCE);
    const session = new MCPSession(request, tok.access_token);
    await session.initialize();
    expect(session.session, "initialize minted a session id").toBeTruthy();
    // The MCP server labels a session with the verified token's subject.
    const agent = String(claims(tok.access_token).sub ?? "");
    expect(agent, "the token names a subject").toBeTruthy();

    await loginAsAdmin(page);
    await gotoSettled(page, MCP_PAGE);

    // The row shows a prefix of the id and carries the whole one as its title.
    const cell = page.getByTitle(session.session, { exact: true });
    await expect(cell).toBeVisible({ timeout: SESSION_LISTED_MS });
    await cell.click();

    const panel = page.getByRole("dialog", { name: "Session" });
    await expect(panel).toBeVisible();
    await expect(panel).toContainText(session.session);
    await expect(page).toHaveURL(
      new RegExp(
        `[?&]${SESSIONS_PARAM.replace(".", "\\.")}=${session.session}(&|$)`,
      ),
    );

    const agentField = panel.getByText("Agent", { exact: true }).locator("..");
    await expect(agentField).toContainText(agent);
    const connected = panel
      .getByText(CONNECTED_FOR, { exact: true })
      .locator("..");
    await expect(connected).toBeVisible();
    await expect(connected).not.toContainText(NO_DURATION);
    await expect(connected).toHaveText(new RegExp(`^${CONNECTED_FOR}.+`));
  });
});
