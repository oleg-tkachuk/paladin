import { NextResponse } from "next/server";
import { afterEach, describe, expect, it, vi } from "vitest";

// No live session: requireSession hands back its 401.
vi.mock("@/lib/auth/session", () => ({
  requireSession: async () =>
    NextResponse.json({ error: "not authenticated" }, { status: 401 }),
}));

import { GET } from "./route";

// The aggregator attaches the server-held snapshot token itself, so a caller
// without a session must be turned away before any backend is asked.
describe("health aggregator without a session", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("answers 401 and polls no backend", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const res = await GET(
      new Request("http://console/api/health/all") as never,
    );
    expect(res.status).toBe(401);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
