import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@/test/utils";
import { Code, ConnectError } from "@connectrpc/connect";

const h = vi.hoisted(() => ({
  inspect: vi.fn(),
  list: vi.fn(),
  status: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  mcpInspectClient: {
    inspect: h.inspect,
    listSessions: h.list,
    getBridgeStatus: h.status,
  },
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

vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

import MCPInspectPage from "./page";
import MCPToolsPage from "./tools/page";
import MCPAccessPage from "./access/page";
import MCPConnectPage from "./connect/page";

const tool = (name: string, audience: string, mutates = false) => ({
  name,
  audience,
  mutates,
  description: `${name} does a thing`,
  capabilityOp: "",
});

const INSPECT = {
  toolCatalog: [
    tool("paladin_list_tenants", "admin"),
    tool("paladin_query_objects", "data"),
    tool("paladin_delete_object", "data", true),
    tool("paladin_create_user", "iam", true),
  ],
  profiles: [
    {
      name: "read_only",
      source: "builtin",
      rawPatterns: ["paladin_list_*", "paladin_query_*"],
      deny: [],
      tools: ["paladin_list_tenants", "paladin_query_objects"],
    },
    {
      name: "admin",
      source: "builtin",
      rawPatterns: ["*"],
      deny: [],
      tools: [
        "paladin_list_tenants",
        "paladin_query_objects",
        "paladin_delete_object",
      ],
    },
  ],
  alwaysDeny: ["paladin_create_user"],
  transports: {
    http: {
      enabled: true,
      addr: ":8095",
      profile: "read_only",
      sessionTimeoutSeconds: 600,
    },
    stdio: { enabled: false, profile: "" },
  },
};

// An open record is kept in the URL, so every test starts at a bare address.
const TOOLS_PATH = "/mcp/tools";

beforeEach(() => {
  window.history.replaceState(null, "", "/mcp");
  h.inspect.mockReset().mockResolvedValue(INSPECT);
  h.list.mockReset().mockResolvedValue({ sessions: [] });
  h.status.mockReset().mockResolvedValue({
    reachable: true,
    error: "",
    upstreams: [{ name: "admin", url: "https://admin", reachable: true }],
    sessions: 0,
  });
});

describe("MCPInspectPage", () => {
  // The configuration failing to load used to render as "No profiles
  // configured" and "Operator opted out of the built-in safe defaults".
  it("says the configuration did not load instead of showing it empty", async () => {
    h.inspect.mockRejectedValue(new ConnectError("fetch failed", Code.Unknown));
    render(<MCPInspectPage />);
    expect(await screen.findByText(/fetch failed/)).toBeInTheDocument();
    expect(screen.queryByText(/No profiles/)).toBeNull();
    expect(screen.queryByText(/always-deny list is empty/)).toBeNull();
  });

  it("shows how many tools agents on HTTP see", async () => {
    render(<MCPInspectPage />);
    expect(await screen.findByText("of 4 tools")).toBeInTheDocument();
  });

  it("lists the sessions on the overview, not the configuration", async () => {
    render(<MCPInspectPage />);
    expect(
      await screen.findByText(/No agent is connected/),
    ).toBeInTheDocument();
    expect(screen.queryByRole("tab")).toBeNull();
  });
});

describe("MCP tools page", () => {
  it("filters the catalog to the HTTP profile by default, and by search", async () => {
    render(<MCPToolsPage />);
    const table = await screen.findByRole("table");
    expect(
      within(table).getByText("paladin_query_objects"),
    ).toBeInTheDocument();
    expect(within(table).queryByText("paladin_delete_object")).toBeNull();

    fireEvent.change(screen.getByLabelText("Search tools"), {
      target: { value: "tenants" },
    });
    expect(within(table).getByText("paladin_list_tenants")).toBeInTheDocument();
    expect(within(table).queryByText("paladin_query_objects")).toBeNull();
  });

  it("opens a tool's record with the profiles that offer it", async () => {
    render(<MCPToolsPage />);
    fireEvent.click(
      await screen.findByRole("row", { name: /paladin_query_objects/ }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("paladin_query_objects does a thing");
    expect(within(dialog).getByText("read_only")).toBeInTheDocument();
    expect(within(dialog).getByText("admin")).toBeInTheDocument();
  });

  it("opens a profile's record with every pattern and tool", async () => {
    render(<MCPToolsPage />);
    fireEvent.mouseDown(await screen.findByRole("tab", { name: "Profiles" }));
    fireEvent.click(screen.getByRole("tab", { name: "Profiles" }));
    fireEvent.click(await screen.findByRole("row", { name: /^read_only/ }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("paladin_query_*");
    expect(dialog).toHaveTextContent("Tools (2 of 4)");
  });

  it("lands on the Profiles tab from a link to a profile", async () => {
    window.history.replaceState(null, "", `${TOOLS_PATH}?profiles.id=admin`);
    render(<MCPToolsPage />);
    const panel = await screen.findByRole("dialog", { name: "Profile" });
    expect(panel).toHaveAccessibleDescription("admin");
    expect(screen.getByRole("tab", { name: "Profiles" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("lands on the Profiles tab from a link to a denied tool", async () => {
    window.history.replaceState(
      null,
      "",
      `${TOOLS_PATH}?denied.id=paladin_create_user`,
    );
    render(<MCPToolsPage />);
    const panel = await screen.findByRole("dialog", { name: "Tool" });
    expect(panel).toHaveTextContent("always denied");
  });

  // The catalog and the profiles share the page: the catalog's open tool
  // opens nothing on the other tab, and is still in the URL to come back to.
  it("keeps the catalog's open tool to itself", async () => {
    window.history.replaceState(
      null,
      "",
      `${TOOLS_PATH}?tools.id=paladin_query_objects`,
    );
    render(<MCPToolsPage />);
    expect(
      await screen.findByRole("dialog", { name: "Tool" }),
    ).toHaveAccessibleDescription("paladin_query_objects");
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Profiles" }));
    fireEvent.click(screen.getByRole("tab", { name: "Profiles" }));
    expect(
      await screen.findByRole("heading", { name: "Always denied" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(window.location.search).toBe("?tools.id=paladin_query_objects");
  });

  // The always-deny list applies after every profile, so it is shown with them.
  it("shows the always-deny list beside the profiles", async () => {
    render(<MCPToolsPage />);
    fireEvent.mouseDown(await screen.findByRole("tab", { name: "Profiles" }));
    fireEvent.click(screen.getByRole("tab", { name: "Profiles" }));
    expect(
      await screen.findByRole("heading", { name: "Always denied" }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("paladin_create_user").length).toBeGreaterThan(
      0,
    );
    // A long name wraps rather than pushing the plane chip off a phone.
    for (const cell of screen.getAllByText("paladin_create_user")) {
      if (cell.tagName === "TD") expect(cell).toHaveClass("break-all");
    }
  });
});

describe("MCP access and connect pages", () => {
  it("shows how agents authorize", async () => {
    render(<MCPAccessPage />);
    expect(await screen.findByText("How agents authorize")).toBeInTheDocument();
  });

  it("shows the transports", async () => {
    render(<MCPConnectPage />);
    expect(await screen.findByText("Streamable HTTP")).toBeInTheDocument();
    expect(screen.getByText("stdio")).toBeInTheDocument();
  });

  it("says the configuration did not load on every page", async () => {
    h.inspect.mockRejectedValue(new ConnectError("fetch failed", Code.Unknown));
    render(<MCPConnectPage />);
    expect(await screen.findByText(/fetch failed/)).toBeInTheDocument();
  });
});

describe("MCP sessions", () => {
  it("says an empty list is unknown when the server is unreachable", async () => {
    h.status.mockResolvedValue({
      reachable: false,
      error: "dial failed",
      upstreams: [],
    });
    render(<MCPInspectPage />);
    expect(
      await screen.findByText(
        /whether anyone is connected is unknown: dial failed/,
      ),
    ).toBeInTheDocument();
  });

  it("says an empty list is unknown when the status call itself fails", async () => {
    h.status.mockRejectedValue(
      new ConnectError("admin plane down", Code.Unavailable),
    );
    render(<MCPInspectPage />);
    expect(
      await screen.findByText(
        /whether anyone is connected is unknown: admin plane down/,
      ),
    ).toBeInTheDocument();
  });

  it("says nobody is connected when the server answers with nothing", async () => {
    render(<MCPInspectPage />);
    expect(
      await screen.findByText(/No agent is connected/),
    ).toBeInTheDocument();
  });

  it("lists sessions with a shortened id", async () => {
    h.list.mockResolvedValue({
      sessions: [
        {
          id: "ABCDEFGHIJKLMNOP",
          agentSubject: "agent-7",
          toolCallCount: 3n,
          requestCount: 9n,
        },
      ],
    });
    render(<MCPInspectPage />);
    expect(await screen.findByText("agent-7")).toBeInTheDocument();
    expect(screen.getByText("ABCDEFGH…")).toBeInTheDocument();
  });

  it("opens a session's record from its row", async () => {
    h.list.mockResolvedValue({
      sessions: [
        {
          id: "ABCDEFGHIJKLMNOP",
          agentSubject: "agent-7",
          toolCallCount: 3n,
          requestCount: 9n,
        },
      ],
    });
    render(<MCPInspectPage />);
    fireEvent.click(await screen.findByRole("row", { name: /agent-7/ }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("ABCDEFGHIJKLMNOP");
    expect(dialog).toHaveTextContent("Tool calls3");
    expect(window.location.search).toBe("?sessions.id=ABCDEFGHIJKLMNOP");
  });
});
