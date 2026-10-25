import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@/test/utils";

const h = vi.hoisted(() => ({ live: vi.fn() }));
vi.mock("./mcp-status", () => ({ useMcpLive: h.live }));

import { MCPSessions } from "./MCPSessions";

const STARTED_S = 1_700_000_000;
const session = (id: string, agentSubject: string, toolCallCount: bigint) => ({
  id,
  agentSubject,
  startedAt: { seconds: BigInt(STARTED_S) },
  lastSeen: { seconds: 1_700_000_600n },
  toolCallCount,
  requestCount: toolCallCount,
});
const SESSIONS = [
  session("aaaa1111-session", "alice", 2n),
  session("bbbb2222-session", "", 10n),
  session("cccc3333-session", "bob", 7n),
];

const agentColumn = () =>
  within(screen.getByRole("table"))
    .getAllByRole("row")
    .slice(1)
    .map((r) => within(r).getAllByRole("cell")[1].textContent);

const SESSIONS_PATH = "/mcp";

describe("MCPSessions", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", SESSIONS_PATH);
    h.live.mockReturnValue({
      status: { isPending: false, data: { reachable: true } },
      sessions: { isPending: false, data: { sessions: SESSIONS } },
    });
  });

  it("sorts by tool calls as numbers, most first when reversed", () => {
    render(<MCPSessions />);
    const calls = screen.getByRole("button", { name: /^Tool calls/ });
    fireEvent.click(calls);
    fireEvent.click(calls);
    expect(agentColumn()).toEqual(["unknown", "bob", "alice"]);
  });

  it("searches the agent, and says when nothing matches", () => {
    render(<MCPSessions />);
    const search = screen.getByRole("searchbox", { name: "Search sessions" });
    fireEvent.change(search, { target: { value: "BOB" } });
    expect(agentColumn()).toEqual(["bob"]);
    fireEvent.change(search, { target: { value: "nobody" } });
    expect(
      screen.getByText("No session matches the search."),
    ).toBeInTheDocument();
  });

  it("opens a session in a panel, under the sessions' own id", async () => {
    render(<MCPSessions />);
    fireEvent.click(screen.getByRole("row", { name: /bob/ }));
    const panel = await screen.findByRole("dialog", { name: "Session" });
    expect(panel).toHaveAccessibleDescription("cccc3333-session");
    expect(panel).toHaveTextContent("Tool calls7");
    expect(window.location.search).toBe("?sessions.id=cccc3333-session");
  });

  describe("connected for", () => {
    const MS_PER_SECOND = 1000;
    const MINUTE_S = 60;
    const HOUR_S = 60 * MINUTE_S;
    const CONNECTED_S = 2 * HOUR_S + 5 * MINUTE_S;

    // Only Date is faked, so findBy's polling keeps its real timers.
    beforeEach(() => {
      vi.useFakeTimers({ toFake: ["Date"] });
      vi.setSystemTime((STARTED_S + CONNECTED_S) * MS_PER_SECOND);
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    it("counts from the session's start up to now", async () => {
      render(<MCPSessions />);
      fireEvent.click(screen.getByRole("row", { name: /bob/ }));
      const panel = await screen.findByRole("dialog", { name: "Session" });
      expect(
        within(panel).getByText("Connected for").nextElementSibling
          ?.textContent,
      ).toBe("2 h 5 min");
    });
  });

  it("opens the session a link names", async () => {
    window.history.replaceState(
      null,
      "",
      `${SESSIONS_PATH}?sessions.id=aaaa1111-session`,
    );
    render(<MCPSessions />);
    const panel = await screen.findByRole("dialog", { name: "Session" });
    expect(panel).toHaveTextContent("Agentalice");
  });

  // A bare ?id= belongs to some other list, not to the sessions.
  it("ignores an id outside its namespace", () => {
    window.history.replaceState(
      null,
      "",
      `${SESSIONS_PATH}?id=aaaa1111-session`,
    );
    render(<MCPSessions />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("keeps a clicked session open after the live list drops it", async () => {
    const { rerender } = render(<MCPSessions />);
    fireEvent.click(screen.getByRole("row", { name: /bob/ }));
    await screen.findByRole("dialog", { name: "Session" });
    h.live.mockReturnValue({
      status: { isPending: false, data: { reachable: true } },
      sessions: { isPending: false, data: { sessions: SESSIONS.slice(0, 1) } },
    });
    rerender(<MCPSessions />);
    expect(screen.getByRole("dialog", { name: "Session" })).toHaveTextContent(
      "Agentbob",
    );
  });
});
