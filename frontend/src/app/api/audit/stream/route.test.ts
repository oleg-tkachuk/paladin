import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const h = vi.hoisted(() => ({
  exchangeAudience: vi.fn(),
  readSessionCookie: vi.fn(),
}));

vi.mock("@/lib/auth/bff", async (orig) => {
  const actual = await orig<typeof import("@/lib/auth/bff")>();
  return {
    ...actual,
    iamAuthClient: () => ({ exchangeAudience: h.exchangeAudience }),
    readSessionCookie: h.readSessionCookie,
  };
});

import { GET } from "./route";

// The SSE proxy had no test of any kind. It is the shape that hides failures
// best: the browser's EventSource retries on its own, so a proxy that answers
// 502 forever looks to a user like a feed that is merely quiet, and the /audit
// page renders perfectly while its live badge sits at "offline".
//
// What is asserted here is every way the proxy can decline, and the fact that
// it declines with the RIGHT status — 401 means the console re-authenticates,
// 502 means it retries. Getting those the wrong way round turns a recoverable
// session problem into an infinite reconnect loop, or vice versa.

const SSE_BODY = () =>
  new ReadableStream<Uint8Array>({
    start(c) {
      c.enqueue(new TextEncoder().encode(": connected\n\n"));
      c.close();
    },
  });

function upstream(init: {
  ok?: boolean;
  status?: number;
  body?: ReadableStream<Uint8Array> | null;
}) {
  return {
    ok: init.ok ?? true,
    status: init.status ?? 200,
    body: init.body === undefined ? SSE_BODY() : init.body,
  } as unknown as Response;
}

const streamReq = () =>
  new Request("https://app.test/api/audit/stream", { method: "GET" });

describe("GET /api/audit/stream", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    h.exchangeAudience.mockReset();
    h.readSessionCookie.mockReset();
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "error").mockImplementation(() => {});
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("401s with no session cookie, and never reaches upstream", async () => {
    h.readSessionCookie.mockResolvedValue(null);

    const res = await GET(streamReq());
    expect(res.status).toBe(401);
    expect(h.exchangeAudience).not.toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("derives an admin-audience token without rotating the chain", async () => {
    // ExchangeAudience, not RefreshToken. A stream reconnects on every network
    // blip and EventSource does it automatically; rotating the refresh chain
    // per connect would turn a flaky network into a stream of rotations.
    h.readSessionCookie.mockResolvedValue("rt");
    h.exchangeAudience.mockResolvedValue({ accessToken: "admin-access" });
    fetchMock.mockResolvedValue(upstream({}));

    await GET(streamReq());
    expect(h.exchangeAudience).toHaveBeenCalledWith({
      refreshToken: "rt",
      targetAudience: "paladin-admin",
    });
  });

  it("sends the bearer and asks for an event stream", async () => {
    h.readSessionCookie.mockResolvedValue("rt");
    h.exchangeAudience.mockResolvedValue({ accessToken: "admin-access" });
    fetchMock.mockResolvedValue(upstream({}));

    await GET(streamReq());
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toMatch(/\/audit\/stream$/);
    expect(init.headers.Authorization).toBe("Bearer admin-access");
    expect(init.headers.Accept).toBe("text/event-stream");
  });

  it("ties the upstream connection to the browser's", async () => {
    // Without the signal, closing the EventSource leaves the backend
    // subscription open: every page visit would leak one until the pod
    // restarts.
    h.readSessionCookie.mockResolvedValue("rt");
    h.exchangeAudience.mockResolvedValue({ accessToken: "admin-access" });
    fetchMock.mockResolvedValue(upstream({}));

    const req = streamReq();
    await GET(req);
    expect(fetchMock.mock.calls[0][1].signal).toBe(req.signal);
  });

  it("401s when the session cannot mint an admin token", async () => {
    // 401, not 502: the session is the problem, and the console must
    // re-authenticate rather than reconnect forever.
    h.readSessionCookie.mockResolvedValue("rt");
    h.exchangeAudience.mockRejectedValue(new Error("refresh token rejected"));

    const res = await GET(streamReq());
    expect(res.status).toBe(401);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("502s when the admin plane cannot be reached", async () => {
    h.readSessionCookie.mockResolvedValue("rt");
    h.exchangeAudience.mockResolvedValue({ accessToken: "admin-access" });
    fetchMock.mockRejectedValue(new Error("ECONNREFUSED"));

    const res = await GET(streamReq());
    expect(res.status).toBe(502);
  });

  it("passes an upstream 401 through as 401", async () => {
    // The token was minted but the admin plane refused it — a disabled user,
    // or a role that lost admin audience. Reporting 502 here would have the
    // console retry a request that can never succeed.
    h.readSessionCookie.mockResolvedValue("rt");
    h.exchangeAudience.mockResolvedValue({ accessToken: "admin-access" });
    fetchMock.mockResolvedValue(upstream({ ok: false, status: 401 }));

    const res = await GET(streamReq());
    expect(res.status).toBe(401);
  });

  it("maps any other upstream failure to 502", async () => {
    h.readSessionCookie.mockResolvedValue("rt");
    h.exchangeAudience.mockResolvedValue({ accessToken: "admin-access" });
    fetchMock.mockResolvedValue(upstream({ ok: false, status: 503 }));

    const res = await GET(streamReq());
    expect(res.status).toBe(502);
  });

  it("502s on a 200 with no body rather than returning an empty stream", async () => {
    // An EventSource handed an empty 200 reports `open` and then silence,
    // which reads to a user as "nothing is happening" instead of "broken".
    h.readSessionCookie.mockResolvedValue("rt");
    h.exchangeAudience.mockResolvedValue({ accessToken: "admin-access" });
    fetchMock.mockResolvedValue(upstream({ body: null }));

    const res = await GET(streamReq());
    expect(res.status).toBe(502);
  });

  it("pipes the upstream body through with SSE headers", async () => {
    h.readSessionCookie.mockResolvedValue("rt");
    h.exchangeAudience.mockResolvedValue({ accessToken: "admin-access" });
    fetchMock.mockResolvedValue(upstream({}));

    const res = await GET(streamReq());
    expect(res.status).toBe(200);
    expect(res.headers.get("Content-Type")).toBe("text/event-stream");
    // Buffering is what silently breaks SSE behind a proxy: frames arrive in
    // batches minutes late, or not until the stream closes.
    expect(res.headers.get("Cache-Control")).toMatch(/no-cache/);
    expect(res.headers.get("X-Accel-Buffering")).toBe("no");
    expect(await res.text()).toBe(": connected\n\n");
  });
});
