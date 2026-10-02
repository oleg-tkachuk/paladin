import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AUDIENCES } from "@/constants";

// bff.ts caches the iam client and the in-flight rotation map in module scope,
// so each test gets a pristine copy via resetModules + dynamic import.

const refreshToken = vi.fn();

// The generated proto module is stubbed to a bare token: these tests exercise
// the BFF's own wiring (client caching, DTO shaping, rotation error handling),
// not protobuf codegen.
vi.mock("@/gen/paladin/iam/v1/auth_service_pb", () => ({
  AuthService: { typeName: "paladin.iam.v1.AuthService" },
}));

const createGrpcWebTransport = vi.fn(() => ({ kind: "transport" }));
vi.mock("@connectrpc/connect-web", () => ({
  createGrpcWebTransport: (...args: unknown[]) =>
    createGrpcWebTransport(...(args as [])),
}));

const createClient = vi.fn(() => ({ refreshToken }));
vi.mock("@connectrpc/connect", () => ({
  createClient: (...args: unknown[]) => createClient(...(args as [])),
}));

const cookieJarGet = vi.fn();
const incomingHeaders = vi.fn(async () => new Headers());
vi.mock("next/headers", () => ({
  cookies: async () => ({ get: cookieJarGet }),
  headers: () => incomingHeaders(),
}));

type Bff = typeof import("./bff");

async function loadBff(): Promise<Bff> {
  vi.resetModules();
  return import("./bff");
}

/** Stand-in for a NextResponse — only its cookie jar is touched. */
function fakeResponse() {
  const set = vi.fn();
  return { res: { cookies: { set } } as never, set };
}

/** A well-formed RefreshToken reply. */
function tokenPair(over: Record<string, unknown> = {}) {
  return {
    tokens: {
      accessToken: "at-1",
      refreshToken: "rt-2",
      accessExpiresInSeconds: 900n,
      refreshExpiresInSeconds: 86_400n,
      ...over,
    },
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.unstubAllEnvs();
});

describe("SESSION_COOKIE_NAME", () => {
  // One cookie, pinned to the iam audience — the data/admin cookies of the v1
  // design are gone, so a drift here silently breaks every auth route.
  it("is the iam refresh cookie", async () => {
    const bff = await loadBff();
    expect(bff.SESSION_COOKIE_NAME).toBe("paladin_rt_iam");
  });

  it("re-exports refreshCookieName", async () => {
    const bff = await loadBff();
    expect(bff.refreshCookieName(AUDIENCES.admin)).toBe("paladin_rt_admin");
  });
});

describe("iamAuthClient", () => {
  it("builds the transport against the configured IAM URL", async () => {
    vi.stubEnv("PALADIN_IAM_URL", "http://iam.test:8085");
    const bff = await loadBff();

    bff.iamAuthClient();

    expect(createGrpcWebTransport).toHaveBeenCalledWith({
      baseUrl: "http://iam.test:8085",
      fetch: (await import("@/lib/server/upstream")).upstreamFetch,
      interceptors: [expect.any(Function)],
    });
  });

  it("falls back to the in-cluster default when PALADIN_IAM_URL is unset", async () => {
    vi.stubEnv("PALADIN_IAM_URL", "");
    const bff = await loadBff();

    bff.iamAuthClient();

    expect(createGrpcWebTransport).toHaveBeenCalledWith({
      baseUrl: "http://paladin-core:8085",
      fetch: (await import("@/lib/server/upstream")).upstreamFetch,
      interceptors: [expect.any(Function)],
    });
  });

  // Rebuilding the transport per call would leak a connection pool on every
  // request, so the cache is load-bearing rather than cosmetic.
  it("caches the client across calls", async () => {
    const bff = await loadBff();

    const first = bff.iamAuthClient();
    const second = bff.iamAuthClient();

    expect(first).toBe(second);
    expect(createClient).toHaveBeenCalledTimes(1);
    expect(createGrpcWebTransport).toHaveBeenCalledTimes(1);
  });
});

describe("session cookie helpers", () => {
  it("writes the session cookie through the response's own jar", async () => {
    const bff = await loadBff();
    const { res, set } = fakeResponse();

    bff.setSessionCookie(res, "rt-value", { maxAgeSeconds: 3600 });

    expect(set).toHaveBeenCalledTimes(1);
    const [name, value, attrs] = set.mock.calls[0];
    expect(name).toBe("paladin_rt_iam");
    expect(value).toBe("rt-value");
    // httpOnly + SameSite=Strict is what lets the cookie be scoped to "/"
    // without adding JS-access surface.
    expect(attrs).toMatchObject({
      httpOnly: true,
      sameSite: "strict",
      path: "/",
      maxAge: 3600,
    });
  });

  it("marks the cookie secure only in production", async () => {
    vi.stubEnv("NODE_ENV", "production");
    const prod = await loadBff();
    const a = fakeResponse();
    prod.setSessionCookie(a.res, "rt", { maxAgeSeconds: 1 });
    expect(a.set.mock.calls[0][2]).toMatchObject({ secure: true });

    vi.stubEnv("NODE_ENV", "development");
    const dev = await loadBff();
    const b = fakeResponse();
    dev.setSessionCookie(b.res, "rt", { maxAgeSeconds: 1 });
    expect(b.set.mock.calls[0][2]).toMatchObject({ secure: false });
  });

  // Logout must expire the cookie, not merely blank it — maxAge 0 is the
  // instruction that actually removes it from the browser jar.
  it("clears the cookie with an empty value and maxAge 0", async () => {
    const bff = await loadBff();
    const { res, set } = fakeResponse();

    bff.clearSessionCookie(res);

    const [name, value, attrs] = set.mock.calls[0];
    expect(name).toBe("paladin_rt_iam");
    expect(value).toBe("");
    expect(attrs).toMatchObject({ maxAge: 0, httpOnly: true, path: "/" });
  });

  it("reads the cookie value when present", async () => {
    const bff = await loadBff();
    cookieJarGet.mockReturnValue({ value: "rt-from-jar" });

    await expect(bff.readSessionCookie()).resolves.toBe("rt-from-jar");
    expect(cookieJarGet).toHaveBeenCalledWith("paladin_rt_iam");
  });

  it("returns null when the cookie is absent", async () => {
    const bff = await loadBff();
    cookieJarGet.mockReturnValue(undefined);

    await expect(bff.readSessionCookie()).resolves.toBeNull();
  });
});

describe("refreshIamChain", () => {
  it("forwards the token and requested audience, and unwraps the pair", async () => {
    const bff = await loadBff();
    refreshToken.mockResolvedValue(tokenPair());

    const out = await bff.refreshIamChain("rt-1", AUDIENCES.data);

    expect(refreshToken).toHaveBeenCalledWith({
      refreshToken: "rt-1",
      requestedAudience: AUDIENCES.data,
    });
    // bigint TTLs must be narrowed to number or the JSON response breaks.
    expect(out).toEqual({
      accessToken: "at-1",
      refreshToken: "rt-2",
      accessExpiresInSeconds: 900,
      refreshExpiresInSeconds: 86_400,
    });
    expect(typeof out.accessExpiresInSeconds).toBe("number");
  });

  it("rejects when the backend returns no token pair", async () => {
    const bff = await loadBff();
    refreshToken.mockResolvedValue({ tokens: undefined });

    await expect(bff.refreshIamChain("rt-1", AUDIENCES.iam)).rejects.toThrow(
      /no token pair/,
    );
  });

  it("propagates a backend rejection", async () => {
    const bff = await loadBff();
    refreshToken.mockRejectedValue(new Error("refresh token rejected"));

    await expect(bff.refreshIamChain("rt-1", AUDIENCES.iam)).rejects.toThrow(
      "refresh token rejected",
    );
  });

  // This function used to collapse concurrent rotations through an in-process
  // dedup map, so two callers produced one RefreshToken call. It does not any
  // more, and the assertion is inverted deliberately: every call reaches the
  // server, because the server is the only place that can decide a rotation
  // race in a way two replicas agree on. The loser gets Aborted and
  // /api/auth/me derives an access token instead — see its own tests.
  it("sends every rotation to the server rather than collapsing them", async () => {
    const bff = await loadBff();
    refreshToken.mockResolvedValue(tokenPair());

    await Promise.all([
      bff.refreshIamChain("rt-same", AUDIENCES.iam),
      bff.refreshIamChain("rt-same", AUDIENCES.iam),
    ]);

    expect(refreshToken).toHaveBeenCalledTimes(2);
  });

  it("retries after a failed rotation", async () => {
    const bff = await loadBff();
    refreshToken
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce(tokenPair());

    await expect(bff.refreshIamChain("rt-x", AUDIENCES.iam)).rejects.toThrow(
      "boom",
    );
    await expect(
      bff.refreshIamChain("rt-x", AUDIENCES.iam),
    ).resolves.toMatchObject({ accessToken: "at-1" });
    expect(refreshToken).toHaveBeenCalledTimes(2);
  });
});

describe("toUserDTO", () => {
  const user = {
    userId: "u-1",
    tenantId: "t-1",
    subject: "alice@example.com",
    displayName: "Alice",
    roles: ["admin", "viewer"],
    resourceVersion: "7",
  };

  it("copies the browser-facing fields", async () => {
    const bff = await loadBff();

    expect(bff.toUserDTO(user, "acme")).toEqual({
      userId: "u-1",
      tenantId: "t-1",
      tenantSlug: "acme",
      subject: "alice@example.com",
      displayName: "Alice",
      roles: ["admin", "viewer"],
      resourceVersion: "7",
    });
  });

  it("defaults tenantSlug to an empty string", async () => {
    const bff = await loadBff();
    expect(bff.toUserDTO(user).tenantSlug).toBe("");
  });

  // The DTO exists to strip proto-runtime fields so AuthContext can
  // JSON.stringify it — a leaked $typeName would break that.
  it("drops proto runtime fields", async () => {
    const bff = await loadBff();
    const dto = bff.toUserDTO({
      ...user,
      $typeName: "paladin.iam.v1.User",
      createdAt: { seconds: 1n },
    } as never);

    expect(dto).not.toHaveProperty("$typeName");
    expect(dto).not.toHaveProperty("createdAt");
    expect(() => JSON.stringify(dto)).not.toThrow();
  });
});

describe("toAccessTokenDTO", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:00:00.000Z"));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("converts a TTL in seconds to an absolute epoch-ms deadline", async () => {
    const bff = await loadBff();

    expect(bff.toAccessTokenDTO(AUDIENCES.data, "tok", 900)).toEqual({
      audience: AUDIENCES.data,
      token: "tok",
      expiresAt: Date.now() + 900_000,
    });
  });

  // The proto carries TTLs as bigint; tokenStore does arithmetic on expiresAt,
  // so it must come out as a plain number.
  it("accepts a bigint TTL and yields a number", async () => {
    const bff = await loadBff();
    const dto = bff.toAccessTokenDTO(AUDIENCES.admin, "tok", 60n);

    expect(dto.expiresAt).toBe(Date.now() + 60_000);
    expect(typeof dto.expiresAt).toBe("number");
  });

  it("handles a zero TTL as immediate expiry", async () => {
    const bff = await loadBff();
    expect(bff.toAccessTokenDTO(AUDIENCES.iam, "tok", 0).expiresAt).toBe(
      Date.now(),
    );
  });
});

describe("forwardClientChain", () => {
  // The IAM plane keys the login rate limiter and the audit source on the
  // client address it resolves from this header. Without it every login from
  // the console came from the console pod, and shared one bucket.
  async function send(incoming: Headers, preset?: string) {
    incomingHeaders.mockResolvedValueOnce(incoming);
    const bff = await loadBff();
    const header = new Headers(preset ? { "X-Forwarded-For": preset } : {});
    const next = vi.fn(async () => ({}));
    await bff.forwardClientChain(next as never)({ header } as never);
    return header.get("X-Forwarded-For");
  }

  it("copies the browser request's chain onto the IAM call", async () => {
    expect(
      await send(new Headers({ "x-forwarded-for": "198.51.100.7, 10.0.0.2" })),
    ).toBe("198.51.100.7, 10.0.0.2");
  });

  it("adds nothing when the browser request had none", async () => {
    expect(await send(new Headers())).toBeNull();
  });

  it("leaves a header the caller set alone", async () => {
    expect(
      await send(
        new Headers({ "x-forwarded-for": "198.51.100.7" }),
        "203.0.113.1",
      ),
    ).toBe("203.0.113.1");
  });

  it("goes out without one outside a request", async () => {
    incomingHeaders.mockRejectedValueOnce(
      new Error("headers() outside a request scope"),
    );
    const bff = await loadBff();
    const header = new Headers();
    await bff.forwardClientChain((async () => ({})) as never)({
      header,
    } as never);
    expect(header.get("X-Forwarded-For")).toBeNull();
  });
});
