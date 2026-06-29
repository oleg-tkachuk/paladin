import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@/test/utils";

// Characterization net for the MCP Bridge page. Read-only inspector — mock the
// inspect RPC and pin the shell + the on-mount fetch.
const h = vi.hoisted(() => ({ inspect: vi.fn() }));

vi.mock("@/lib/connect/client", () => ({
  mcpInspectClient: { inspect: h.inspect },
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({
    title,
    actions,
  }: {
    title: string;
    actions?: React.ReactNode;
  }) => (
    <div>
      <h1>{title}</h1>
      {actions}
    </div>
  ),
}));

import MCPInspectPage from "./page";

beforeEach(() => {
  h.inspect.mockResolvedValue({
    profiles: [],
    toolCatalog: [],
    alwaysDeny: [],
    upstreams: undefined,
    transports: undefined,
  });
});

describe("MCPInspectPage", () => {
  it("renders the header and a refresh action", () => {
    render(<MCPInspectPage />);
    expect(
      screen.getByRole("heading", { name: "MCP Bridge" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Refresh/i }),
    ).toBeInTheDocument();
  });

  it("inspects the bridge on mount", async () => {
    render(<MCPInspectPage />);
    await waitFor(() => expect(h.inspect).toHaveBeenCalled());
  });
});
