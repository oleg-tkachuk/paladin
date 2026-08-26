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
