import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { ConnectError, Code } from "@connectrpc/connect";

// The shell fans out to two planes and reports each section's status
// separately, so one failing badge greys instead of failing the page. That is
// the right design and it is also the most convincing way to hide a permanent
// break: a section that has NEVER worked renders exactly like a service that
// is briefly down.
//
// So these tests pin two things. The isolation — a failure in one section
// must not touch the others — and the reverse, which is the part that catches
// the hidden break: with everything reachable, every section must come back
// `ok`. The e2e in tests/e2e/shell.spec.ts asserts the same thing against a
// live cluster, because a mock cannot tell you whether the RPC still exists.

const h = vi.hoisted(() => ({
  getVersion: vi.fn(),
  getHealth: vi.fn(),
  listOperations: vi.fn(),
}));

vi.mock("@connectrpc/connect", async (orig) => {
  const actual = await orig<typeof import("@connectrpc/connect")>();
  return {
    ...actual,
    // One client factory for both services; the route builds two clients and
    // each only calls its own methods, so a single shape serves both.
    createClient: () => ({
      getVersion: h.getVersion,
      getHealth: h.getHealth,
      listOperations: h.listOperations,
    }),
  };
});

vi.mock("@bufbuild/protobuf", async (orig) => {
  const actual = await orig<typeof import("@bufbuild/protobuf")>();
  return {
    ...actual,
    // The route re-encodes each response to JSON through the generated
    // schema. What is under test is the fan-out and the gating, not codegen.
    toJson: (_schema: unknown, msg: unknown) => msg as never,
  };
});

vi.mock("@/lib/server/upstream", () => ({ planeTransport: () => ({}) }));

import { GET } from "./route";

// Minimal unsigned JWT carrying an `aud` claim — the gate only base64url-
// decodes the payload; the backend verifies signatures.
function jwt(aud: string): string {
  const payload = Buffer.from(JSON.stringify({ aud })).toString("base64url");
  return `eyJhbGciOiJIUzI1NiJ9.${payload}.sig`;
}

const ADMIN_TOKEN = jwt("paladin-admin");
const IAM_TOKEN = jwt("paladin-iam");

function shellReq(headers: Record<string, string> = {}): Request {
  return new Request("https://app.test/api/shell", { method: "GET", headers });
}

const bothTokens = () => ({
  Authorization: `Bearer ${ADMIN_TOKEN}`,
  "X-Paladin-Iam-Authorization": `Bearer ${IAM_TOKEN}`,
});

type Body = Record<string, { status: string; reason?: string; data?: unknown }>;

describe("GET /api/shell", () => {
  beforeEach(() => {
    h.getVersion.mockReset().mockResolvedValue({ version: "1.2.3" });
    h.getHealth.mockReset().mockResolvedValue({ status: "SERVING" });
    h.listOperations.mockReset().mockResolvedValue({ operations: [] });
    vi.spyOn(console, "warn").mockImplementation(() => {});
  });
  afterEach(() => vi.restoreAllMocks());

  it("returns every section ok when both planes answer", async () => {
    // The assertion that catches a section broken forever. Everything is
    // reachable here, so anything less than three `ok` is a defect in the
    // route rather than in the environment.
    const res = await GET(shellReq(bothTokens()));
    expect(res.status).toBe(200);
    const body = (await res.json()) as Body;
    expect(Object.keys(body).sort()).toEqual([
      "health",
      "operations",
      "version",
    ]);
    for (const [name, section] of Object.entries(body)) {
      expect(section.status, `${name}: ${section.reason ?? ""}`).toBe("ok");
    }
  });

  it("greys one section without touching the others", async () => {
    h.getHealth.mockRejectedValue(
      new ConnectError("health plane down", Code.Unavailable),
    );

    const body = (await (await GET(shellReq(bothTokens()))).json()) as Body;
    expect(body.health.status).toBe("unavailable");
    expect(body.health.reason).toMatch(/health plane down/);
    // The whole point of splitting them.
    expect(body.version.status).toBe("ok");
    expect(body.operations.status).toBe("ok");
  });

  it("still answers 200 when every section fails", async () => {
    // A page whose chrome is entirely unavailable must still render; turning
    // this into a 500 would take the console down with one plane.
    h.getVersion.mockRejectedValue(new Error("boom"));
    h.getHealth.mockRejectedValue(new Error("boom"));
    h.listOperations.mockRejectedValue(new Error("boom"));

    const res = await GET(shellReq(bothTokens()));
    expect(res.status).toBe(200);
    const body = (await res.json()) as Body;
    expect(Object.values(body).every((s) => s.status === "unavailable")).toBe(
      true,
    );
  });

  it("401s without an Authorization header, and calls nothing", async () => {
    const res = await GET(shellReq());
    expect(res.status).toBe(401);
    expect(h.listOperations).not.toHaveBeenCalled();
  });

  it("403s an admin-plane call carrying another plane's audience", async () => {
    // This route reaches cluster-only planes; it must not become a way around
    // the audience gate the RPC bridge applies.
    const res = await GET(
      shellReq({ Authorization: `Bearer ${jwt("paladin-data")}` }),
    );
    expect(res.status).toBe(403);
    expect(h.listOperations).not.toHaveBeenCalled();
  });

  it("names the missing iam token instead of forwarding the admin one", async () => {
    // Sending the admin token to the iam plane would make both badges
    // permanently unavailable with an authentication error — and the plane
    // would be right to refuse it. The reason has to say what is missing.
    const body = (await (
      await GET(shellReq({ Authorization: `Bearer ${ADMIN_TOKEN}` }))
    ).json()) as Body;

    expect(body.version.status).toBe("unavailable");
    expect(body.version.reason).toMatch(/X-Paladin-Iam-Authorization/);
    expect(body.health.reason).toMatch(/X-Paladin-Iam-Authorization/);
    expect(h.getVersion).not.toHaveBeenCalled();
    // The admin-plane section is unaffected by a missing iam token.
    expect(body.operations.status).toBe("ok");
  });

  it("refuses an iam token with the wrong audience rather than sending it", async () => {
    const body = (await (
      await GET(
        shellReq({
          Authorization: `Bearer ${ADMIN_TOKEN}`,
          "X-Paladin-Iam-Authorization": `Bearer ${jwt("paladin-data")}`,
        }),
      )
    ).json()) as Body;

    expect(body.version.status).toBe("unavailable");
    expect(body.version.reason).toMatch(/audience/);
    expect(h.getVersion).not.toHaveBeenCalled();
  });

  it("forwards each plane its own token", async () => {
    await GET(shellReq(bothTokens()));
    expect(h.getVersion.mock.calls[0][1].headers.Authorization).toBe(
      `Bearer ${IAM_TOKEN}`,
    );
    expect(h.listOperations.mock.calls[0][1].headers.Authorization).toBe(
      `Bearer ${ADMIN_TOKEN}`,
    );
  });

  it("asks for the NEWEST operations, not the oldest", async () => {
    // The cursor is ascending by default, so a page of 50 is the 50 OLDEST
    // operations — which is how a three-day-old failure became the drawer's
    // idea of current activity.
    await GET(shellReq(bothTokens()));
    const req = h.listOperations.mock.calls[0][0];
    expect(req.sortOrder).toBe(2); // SortOrder.DESC
    expect(req.page.pageSize).toBe(50);
  });

  it("is never cached", async () => {
    // A cached shell shows a stale health badge for as long as the cache
    // lives, which is indistinguishable from a stuck service.
    const res = await GET(shellReq(bothTokens()));
    expect(res.headers.get("Cache-Control")).toBe("no-store");
  });
});
