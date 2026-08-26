import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({ list: vi.fn(), status: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  mcpInspectClient: { listSessions: h.list, getBridgeStatus: h.status },
}));

import { MCPLiveSessions } from "./MCPLiveSessions";

describe("MCPLiveSessions empty state", () => {
  beforeEach(() => {
    h.list.mockReset();
    h.status.mockReset();
  });

  // The defect this component now covers: ListSessions degrades to an empty
  // list when the bridge is unreachable, so a dead bridge used to render as a
  // tidy "no active sessions" — on the page an operator opens when something
  // is wrong.
  it("says the list is unknown when the bridge is unreachable", async () => {
    h.list.mockResolvedValue({ sessions: [] });
    h.status.mockResolvedValue({
      reachable: false,
      error: "connection refused",
    });

    render(<MCPLiveSessions />);

    expect(await screen.findByText(/could not be reached/i)).toBeVisible();
    expect(screen.getByText(/connection refused/)).toBeVisible();
    expect(screen.queryByText(/No active sessions/i)).toBeNull();
  });

  it("says the server is idle when the bridge answers with nothing", async () => {
    h.list.mockResolvedValue({ sessions: [] });
    h.status.mockResolvedValue({ reachable: true, error: "" });

    render(<MCPLiveSessions />);

    expect(await screen.findByText(/No active sessions/i)).toBeVisible();
    expect(screen.queryByText(/could not be reached/i)).toBeNull();
  });

  // A status call that itself fails must not be read as "bridge fine".
  it("treats an unanswerable status call as unreachable", async () => {
    h.list.mockResolvedValue({ sessions: [] });
    h.status.mockRejectedValue(new Error("boom"));

    render(<MCPLiveSessions />);

    expect(await screen.findByText(/could not be reached/i)).toBeVisible();
  });
});
