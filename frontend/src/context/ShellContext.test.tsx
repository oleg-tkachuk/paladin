import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({ token: vi.fn() }));
vi.mock("@/lib/auth/tokenStore", () => ({ getAccessToken: h.token }));

import { ShellProvider, useShell } from "./ShellContext";

// Renders every section's status so one assertion can read the whole shell.
function Probe() {
  const shell = useShell();
  return (
    <div>
      <span data-testid="version">{shell.version.status}</span>
      <span data-testid="health">{shell.health.status}</span>
      <span data-testid="operations">{shell.operations.status}</span>
      <span data-testid="ops-count">{shell.operations.data?.length ?? -1}</span>
      <span data-testid="health-reason">{shell.health.reason ?? ""}</span>
    </div>
  );
}

function mockShellResponse(body: unknown, ok = true) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({
      ok,
      status: ok ? 200 : 500,
      headers: new Headers({ "content-type": "application/json" }),
      json: async () => body,
    }),
  );
}

describe("ShellContext", () => {
  beforeEach(() => {
    h.token.mockReset();
    h.token.mockResolvedValue("tok");
    vi.unstubAllGlobals();
  });

  // The point of the aggregate: one section being down must not cost the
  // others. A single failed fetch used to take the whole chrome with it.
  it("keeps healthy sections when one is unavailable", async () => {
    mockShellResponse({
      version: { status: "ok", data: { version: "1.2.3" } },
      health: { status: "unavailable", reason: "UNAVAILABLE: no route" },
      operations: { status: "ok", data: { operations: [] } },
    });

    render(
      <ShellProvider>
        <Probe />
      </ShellProvider>,
    );

    await waitFor(() =>
      expect(screen.getByTestId("version")).toHaveTextContent("ok"),
    );
    expect(screen.getByTestId("health")).toHaveTextContent("unavailable");
    expect(screen.getByTestId("health-reason")).toHaveTextContent("no route");
    expect(screen.getByTestId("operations")).toHaveTextContent("ok");
    expect(screen.getByTestId("ops-count")).toHaveTextContent("0");
  });

  // A section whose payload does not decode is that section's problem.
  it("degrades one section on a malformed payload", async () => {
    mockShellResponse({
      version: { status: "ok", data: { notAVersionField: true } },
      health: { status: "ok", data: {} },
      operations: { status: "ok", data: { operations: [] } },
    });

    render(
      <ShellProvider>
        <Probe />
      </ShellProvider>,
    );

    await waitFor(() =>
      expect(screen.getByTestId("version")).toHaveTextContent("unavailable"),
    );
    expect(screen.getByTestId("health")).toHaveTextContent("ok");
  });

  // The aggregate itself being down is not "everything is fine and empty".
  it("marks every section unavailable when the call fails", async () => {
    mockShellResponse({}, false);

    render(
      <ShellProvider>
        <Probe />
      </ShellProvider>,
    );

    await waitFor(() =>
      expect(screen.getByTestId("operations")).toHaveTextContent("unavailable"),
    );
    expect(screen.getByTestId("version")).toHaveTextContent("unavailable");
    expect(screen.getByTestId("health")).toHaveTextContent("unavailable");
    expect(screen.getByTestId("ops-count")).toHaveTextContent("-1");
  });
});

// The version and health sections live on the iam plane, the operations
// section on admin, and each plane verifies its own audience. Sending one
// token to both would make the health badge permanently unavailable with an
// authentication error — and the plane would be right to refuse it.
describe("ShellContext tokens", () => {
  beforeEach(() => {
    h.token.mockReset();
    vi.unstubAllGlobals();
  });

  it("sends the admin and iam tokens on their own headers", async () => {
    h.token.mockImplementation(async (aud: string) => `tok-${aud}`);
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ "content-type": "application/json" }),
      json: async () => ({
        version: { status: "ok", data: {} },
        health: { status: "ok", data: {} },
        operations: { status: "ok", data: { operations: [] } },
      }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ShellProvider>
        <Probe />
      </ShellProvider>,
    );

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const init = fetchMock.mock.calls[0][1] as {
      headers: Record<string, string>;
    };
    expect(init.headers.Authorization).toMatch(/^Bearer tok-/);
    expect(init.headers["X-Paladin-Iam-Authorization"]).toMatch(/^Bearer tok-/);
    expect(init.headers.Authorization).not.toBe(
      init.headers["X-Paladin-Iam-Authorization"],
    );
  });

  // A failed iam exchange costs the two sections that need it, not the call.
  it("still asks for the shell when the iam token cannot be minted", async () => {
    h.token.mockImplementation(async (aud: string) => {
      if (aud.includes("iam")) throw new Error("exchange failed");
      return "tok-admin";
    });
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ "content-type": "application/json" }),
      json: async () => ({
        version: { status: "unavailable", reason: "missing token" },
        health: { status: "unavailable", reason: "missing token" },
        operations: { status: "ok", data: { operations: [] } },
      }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ShellProvider>
        <Probe />
      </ShellProvider>,
    );

    await waitFor(() =>
      expect(screen.getByTestId("operations")).toHaveTextContent("ok"),
    );
    const init = fetchMock.mock.calls[0][1] as {
      headers: Record<string, string>;
    };
    expect(init.headers["X-Paladin-Iam-Authorization"]).toBeUndefined();
    expect(screen.getByTestId("health")).toHaveTextContent("unavailable");
  });
});

describe("ShellContext session", () => {
  beforeEach(() => {
    h.token.mockReset();
    vi.unstubAllGlobals();
  });
  // An expired session is redirected to the login page by the edge middleware,
  // and fetch follows redirects — so a 200 here can be an HTML page.
  it("reports a signed-out session instead of a JSON parse error", async () => {
    h.token.mockResolvedValue("tok");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        headers: new Headers({ "content-type": "text/html; charset=utf-8" }),
        json: async () => {
          throw new SyntaxError("Unexpected token '<'");
        },
      }),
    );

    render(
      <ShellProvider>
        <Probe />
      </ShellProvider>,
    );

    await waitFor(() =>
      expect(screen.getByTestId("health")).toHaveTextContent("unavailable"),
    );
    expect(screen.getByTestId("health-reason")).toHaveTextContent(
      "not signed in",
    );
  });
});
