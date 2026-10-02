import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// A live session; session.test.ts covers the refusal.
vi.mock("@/lib/auth/session", () => ({ requireSession: async () => null }));

import { GET, HEALTH_ROLES_ENV } from "./route";

// Every snapshot fetch answers healthy for the role in its URL, so the
// response lists exactly the roles the route chose to poll.
function stubFetch() {
  const fetchMock = vi.fn(async (url: string) => {
    const role = new URL(url).hostname;
    return new Response(
      JSON.stringify({
        role,
        status: "healthy",
        components: [],
        checked_at: "",
      }),
      { status: 200 },
    );
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

async function polled(): Promise<string[]> {
  const res = await GET(new Request("http://console/api/health/all") as never);
  const body = (await res.json()) as { roles: { role: string }[] };
  return body.roles.map((r) => r.role).sort();
}

describe("health aggregator role selection", () => {
  beforeEach(() => {
    for (const [role, key] of [
      ["api", "PALADIN_IAM_URL"],
      ["admin", "PALADIN_ADMIN_URL"],
      ["worker", "PALADIN_WORKER_URL"],
      ["mcp", "PALADIN_MCP_URL"],
      ["dispatcher", "PALADIN_DISPATCHER_URL"],
      ["ingest", "PALADIN_INGEST_URL"],
    ]) {
      vi.stubEnv(key, `http://${role}:1`);
    }
  });
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.unstubAllGlobals();
  });

  it("polls every role when the list is unset", async () => {
    stubFetch();
    expect(await polled()).toEqual([
      "admin",
      "api",
      "dispatcher",
      "ingest",
      "mcp",
      "worker",
    ]);
  });

  it("polls only the listed roles", async () => {
    vi.stubEnv(HEALTH_ROLES_ENV, "api, admin,worker");
    const fetchMock = stubFetch();
    expect(await polled()).toEqual(["admin", "api", "worker"]);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("ignores names that are not roles and empty entries", async () => {
    vi.stubEnv(HEALTH_ROLES_ENV, "api,,nope");
    stubFetch();
    expect(await polled()).toEqual(["api"]);
  });

  it("polls nothing for an empty list", async () => {
    vi.stubEnv(HEALTH_ROLES_ENV, "");
    const fetchMock = stubFetch();
    expect(await polled()).toEqual([]);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
