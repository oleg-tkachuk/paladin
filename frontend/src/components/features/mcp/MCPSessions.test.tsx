import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@/test/utils";

const h = vi.hoisted(() => ({ live: vi.fn() }));
vi.mock("./mcp-status", () => ({ useMcpLive: h.live }));

import { MCPSessions } from "./MCPSessions";

const session = (id: string, agentSubject: string, toolCallCount: bigint) => ({
  id,
  agentSubject,
  startedAt: { seconds: 1_700_000_000n },
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

describe("MCPSessions", () => {
  beforeEach(() => {
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
});
