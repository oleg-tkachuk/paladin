import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConnectError, Code } from "@connectrpc/connect";
import { fireEvent, render, screen, waitFor } from "@/test/utils";

import { BANNER_PATH } from "@/constants";

const h = vi.hoisted(() => ({
  getVersion: vi.fn(),
  getHealth: vi.fn(),
  listOperations: vi.fn(),
  roles: ["platform.admin"] as string[],
  signedIn: true,
}));
// The hidden-tab watch reads one preference; off here.
vi.mock("@/hooks/usePreference", () => ({
  usePreference: () => [false, vi.fn()],
}));
vi.mock("@/lib/connect/client", () => ({
  iamHealthClient: { getVersion: h.getVersion, getHealth: h.getHealth },
  adminOperationClient: { listOperations: h.listOperations },
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({
    user: h.signedIn ? { roles: h.roles, tenantId: "t", userId: "u" } : null,
  }),
}));

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
      <span data-testid="banner">{shell.banner?.content ?? "none"}</span>
      <button type="button" onClick={() => void shell.refresh()}>
        refresh
      </button>
    </div>
  );
}

const renderShell = () =>
  render(
    <ShellProvider>
      <Probe />
    </ShellProvider>,
  );

function bannerAnswers(body: unknown, ok = true) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({
      ok,
      status: ok ? 200 : 500,
      json: async () => body,
    }),
  );
}

const text = (id: string) => screen.getByTestId(id).textContent;

describe("ShellContext", () => {
  beforeEach(() => {
    h.getVersion.mockReset().mockResolvedValue({ version: "1.2.3" });
    h.getHealth.mockReset().mockResolvedValue({ components: [] });
    h.listOperations.mockReset().mockResolvedValue({ operations: [{}, {}] });
    h.roles = ["platform.admin"];
    h.signedIn = true;
    vi.unstubAllGlobals();
    bannerAnswers(null);
  });

  // The provider sits on the login page too: asking there only collects a
  // refusal, and the chrome read "Unknown" after sign-in until the next poll.
  it("asks nothing before anyone is signed in", async () => {
    h.signedIn = false;
    renderShell();
    await new Promise((r) => setTimeout(r, 0));
    expect(h.getVersion).not.toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("reads every section through its own call", async () => {
    renderShell();
    await waitFor(() => expect(text("ops-count")).toBe("2"));
    expect(text("version")).toBe("ok");
    expect(text("health")).toBe("ok");
    expect(fetch).toHaveBeenCalledWith(BANNER_PATH, expect.anything());
  });

  it("folds refreshes that overlap into one fetch", async () => {
    renderShell();
    await waitFor(() => expect(text("version")).toBe("ok"));
    h.getVersion.mockClear();
    fireEvent.click(screen.getByText("refresh"));
    fireEvent.click(screen.getByText("refresh"));
    await waitFor(() => expect(h.getVersion).toHaveBeenCalledTimes(1));
  });

  it("asks a tenant.user's shell for no operations on the admin plane", async () => {
    h.roles = ["tenant.user"];
    renderShell();
    await waitFor(() => expect(text("operations")).toBe("unavailable"));
    expect(h.listOperations).not.toHaveBeenCalled();
    expect(text("version")).toBe("ok");
  });

  it("keeps healthy sections when one is unavailable, and says why", async () => {
    h.getHealth.mockRejectedValue(
      new ConnectError("probe down", Code.Unavailable),
    );
    renderShell();
    await waitFor(() => expect(text("health")).toBe("unavailable"));
    expect(text("health-reason")).toBe("Unavailable: probe down");
    expect(text("version")).toBe("ok");
    expect(text("operations")).toBe("ok");
  });

  it("hands the operator's banner to the chrome", async () => {
    bannerAnswers({ content: "Maintenance at 18:00", severity: "warning" });
    renderShell();
    await waitFor(() => expect(text("banner")).toBe("Maintenance at 18:00"));
  });

  // A maintenance notice is most needed exactly when the console cannot reach
  // what it announces.
  it("keeps the banner when the next poll cannot read it", async () => {
    bannerAnswers({ content: "Maintenance at 18:00", severity: "warning" });
    renderShell();
    await waitFor(() => expect(text("banner")).toBe("Maintenance at 18:00"));
    bannerAnswers({}, false);
    fireEvent.click(screen.getByText("refresh"));
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(text("banner")).toBe("Maintenance at 18:00");
  });
});
