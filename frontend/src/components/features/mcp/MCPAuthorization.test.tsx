import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import {
  fireEvent,
  render,
  screen,
  typeToConfirm,
  waitFor,
  within,
} from "@/test/utils";

import type { MCPInspectResponse } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";
import { MCPAuthorization } from "./MCPAuthorization";
import { MCPConnect } from "./MCPOverview";
import { EffectBadge } from "./MCPCatalog";

const h = vi.hoisted(() => ({ deleteOAuthClient: vi.fn() }));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/lib/connect/client", () => ({
  oauthClientClient: { deleteOAuthClient: h.deleteOAuthClient },
}));

const AGENT = {
  clientId: "paladin-e2e-agent",
  public: true,
  allowedScopes: ["paladin.read"],
  allowedAudiences: ["paladin-mcp"],
  tokenExchange: false,
  skipConsent: false,
  configured: true,
};
const MCP_SERVER = {
  clientId: "paladin-mcp-server",
  public: false,
  allowedScopes: [],
  allowedAudiences: [],
  tokenExchange: true,
  skipConsent: false,
  configured: true,
};
// A client that registered itself, as the store lists it.
const REGISTERED = {
  clientId: "paladin-0199a1b2-dcr",
  public: true,
  allowedScopes: ["paladin.read"],
  allowedAudiences: ["paladin-mcp"],
  tokenExchange: false,
  skipConsent: false,
  dynamic: true,
  configured: false,
};

function inspect(oauth: Partial<MCPInspectResponse["oauth"]>) {
  return {
    transports: { http: { enabled: true, addr: ":8095" } },
    oauth: {
      authorizationServerEnabled: true,
      edgeOauthEnabled: true,
      resourceUrl: "https://mcp.example.test/mcp",
      edgeAudience: "",
      exchangeClientId: "",
      seedClients: [],
      registeredClients: [],
      registeredClientsError: "",
      ...oauth,
    },
  } as unknown as MCPInspectResponse;
}

describe("MCPAuthorization", () => {
  it("names the audience and the client the MCP server exchanges as", () => {
    render(
      <MCPAuthorization
        inspect={inspect({
          edgeAudience: "paladin-mcp",
          exchangeClientId: MCP_SERVER.clientId,
          seedClients: [AGENT, MCP_SERVER],
        } as never)}
      />,
    );
    expect(
      screen.getByText("Accepted audience").closest("[data-slot=card]"),
    ).toHaveTextContent("paladin-mcp");
    expect(screen.getAllByText(MCP_SERVER.clientId)).toHaveLength(2);
    expect(screen.getByText("MCP server")).toBeInTheDocument();
    expect(screen.getByText("public (PKCE)")).toBeInTheDocument();
  });

  it("says a forwarding server takes any plane's token", () => {
    render(<MCPAuthorization inspect={inspect({})} />);
    expect(
      screen.getByText("Accepted audience").closest("[data-slot=card]"),
    ).toHaveTextContent("any plane's");
    expect(screen.getByText("No client is registered.")).toBeInTheDocument();
  });
});

describe("MCPAuthorization client table", () => {
  const clientColumn = () =>
    within(screen.getByRole("table"))
      .getAllByRole("row")
      .slice(1)
      .map((r) => within(r).getAllByRole("cell")[0].textContent);
  const shown = () =>
    render(
      <MCPAuthorization
        inspect={inspect({
          registeredClients: [MCP_SERVER, REGISTERED, AGENT],
        } as never)}
      />,
    );

  it("narrows the clients by source and by kind, counting each", async () => {
    shown();
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by source" }),
    );
    const registered = screen.getByRole("menuitemcheckbox", {
      name: /registered itself/,
    });
    expect(registered).toHaveTextContent("1");
    await userEvent.click(registered);
    await userEvent.keyboard("{Escape}");
    expect(clientColumn()).toEqual([REGISTERED.clientId]);
    expect(screen.getByText("1 of 3")).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by source, 1 chosen" }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /registered itself/ }),
    );
    await userEvent.keyboard("{Escape}");
    expect(clientColumn()).toHaveLength(3);
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by kind" }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /MCP server/ }),
    );
    await userEvent.keyboard("{Escape}");
    expect(clientColumn()).toHaveLength(1);
  });

  it("searches the scopes and audiences too, and sorts by client", () => {
    shown();
    fireEvent.change(
      screen.getByRole("searchbox", { name: "Search OAuth clients" }),
      { target: { value: "paladin-mcp" } },
    );
    // Both agents allow the paladin-mcp audience; the server's id holds it.
    expect(clientColumn()).toHaveLength(3);
    fireEvent.change(
      screen.getByRole("searchbox", { name: "Search OAuth clients" }),
      { target: { value: "" } },
    );
    fireEvent.click(screen.getByRole("button", { name: /^Client/ }));
    expect(clientColumn()).toEqual([
      REGISTERED.clientId,
      AGENT.clientId,
      MCP_SERVER.clientId,
    ]);
  });
});

describe("MCPAuthorization registered clients", () => {
  const DCR = {
    ...AGENT,
    clientId: "paladin-0199-dcr",
    dynamic: true,
  };

  it("lists every registered client and marks the self-registered", () => {
    render(
      <MCPAuthorization
        inspect={inspect({
          seedClients: [AGENT],
          registeredClients: [{ ...AGENT, dynamic: false }, DCR],
        } as never)}
      />,
    );
    expect(screen.getByText(DCR.clientId)).toBeInTheDocument();
    // Counted in the table: the source filter names the source too.
    expect(
      within(screen.getByRole("table")).getAllByText("registered itself"),
    ).toHaveLength(1);
  });

  it("falls back to the configured clients, and says why", () => {
    render(
      <MCPAuthorization
        inspect={inspect({
          seedClients: [AGENT],
          registeredClientsError: "pool closed",
        } as never)}
      />,
    );
    expect(screen.getByText(AGENT.clientId)).toBeInTheDocument();
    expect(
      screen.getByText(/could not be read \(pool closed\)/),
    ).toBeInTheDocument();
  });
});

describe("a client's record", () => {
  it("opens from its row, with what the table leaves out", async () => {
    render(
      <MCPAuthorization
        inspect={inspect({ seedClients: [AGENT, MCP_SERVER] } as never)}
      />,
    );
    fireEvent.click(
      screen.getByRole("row", { name: new RegExp(AGENT.clientId) }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("by the configuration");
    expect(dialog).toHaveTextContent("paladin.read");
    expect(dialog).toHaveTextContent("asked");
  });
});

describe("deleting a client", () => {
  beforeEach(() => {
    h.deleteOAuthClient.mockReset().mockResolvedValue({});
  });

  const openRecord = async (clientId: string) => {
    fireEvent.click(screen.getByRole("row", { name: new RegExp(clientId) }));
    return screen.findByRole("dialog");
  };

  it("deletes a self-registered client only once confirmed", async () => {
    render(
      <MCPAuthorization
        inspect={inspect({
          registeredClients: [MCP_SERVER, REGISTERED],
        } as never)}
      />,
    );
    const record = await openRecord(REGISTERED.clientId);
    expect(record).toHaveTextContent("by the client itself");
    fireEvent.click(
      within(record).getByRole("button", { name: "Delete client" }),
    );
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("every tenant");
    expect(h.deleteOAuthClient).not.toHaveBeenCalled();
    const del = within(confirm).getByRole("button", { name: "Delete client" });
    expect(del).toBeDisabled();

    await typeToConfirm("delete");
    fireEvent.click(del);
    await waitFor(() =>
      expect(h.deleteOAuthClient).toHaveBeenCalledWith({
        clientId: REGISTERED.clientId,
      }),
    );
  });

  it("offers no delete for a client the configuration seeds", async () => {
    render(
      <MCPAuthorization
        inspect={inspect({
          registeredClients: [MCP_SERVER, REGISTERED],
        } as never)}
      />,
    );
    const record = await openRecord(MCP_SERVER.clientId);
    expect(
      within(record).queryByRole("button", { name: "Delete client" }),
    ).toBeNull();
    expect(record).toHaveTextContent("auth.oauth.seed_clients");
  });

  // Listed from the configuration because the store could not be read: there
  // is no row to delete.
  it("offers no delete when the store's clients are unknown", async () => {
    render(
      <MCPAuthorization
        inspect={inspect({
          seedClients: [{ ...REGISTERED, configured: false }],
        } as never)}
      />,
    );
    const record = await openRecord(REGISTERED.clientId);
    expect(
      within(record).queryByRole("button", { name: "Delete client" }),
    ).toBeNull();
  });

  it("names a client the configuration dropped", async () => {
    const dropped = { ...REGISTERED, clientId: "old-agent", dynamic: false };
    render(
      <MCPAuthorization
        inspect={inspect({ registeredClients: [dropped] } as never)}
      />,
    );
    const record = await openRecord(dropped.clientId);
    expect(record).toHaveTextContent("no longer lists it");
    expect(
      within(record).getByRole("button", { name: "Delete client" }),
    ).toBeInTheDocument();
  });
});

describe("MCPConnect", () => {
  it("explains the exchange when the server is its own audience", () => {
    render(
      <MCPConnect
        inspect={inspect({ edgeAudience: "paladin-mcp" } as never)}
      />,
    );
    expect(
      screen.getByText(/one session reaches all of them/),
    ).toBeInTheDocument();
  });

  it("explains single-plane tokens when it forwards them", () => {
    render(<MCPConnect inspect={inspect({})} />);
    expect(screen.getByText(/issued for a single plane/)).toBeInTheDocument();
  });

  it("gives each transport its endpoint and a way to copy it", () => {
    render(
      <MCPConnect
        inspect={inspect({
          edgeAudience: "paladin-mcp",
          resourceUrl: "https://mcp.example/mcp",
        } as never)}
      />,
    );
    expect(
      screen.getByText("POST https://mcp.example/mcp"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Authorization: Bearer <paladin-mcp token>"),
    ).toBeInTheDocument();
    expect(screen.getByTitle("Copy Endpoint")).toBeInTheDocument();
    expect(screen.getByTitle("Copy Command")).toBeInTheDocument();
  });
});

describe("EffectBadge", () => {
  it.each([
    [{ mutates: false, destructive: false, openWorld: false }, ["read"]],
    [
      { mutates: true, destructive: false, openWorld: false },
      ["changes state"],
    ],
    [{ mutates: true, destructive: true, openWorld: false }, ["destructive"]],
    [
      { mutates: true, destructive: true, openWorld: true },
      ["destructive", "open world"],
    ],
  ])("labels %o as %o", (tool, labels) => {
    const { container } = render(<EffectBadge tool={tool} />);
    expect(
      Array.from(container.querySelectorAll("[data-slot=badge]")).map(
        (b) => b.textContent,
      ),
    ).toEqual(labels);
  });
});

// The row reads at one size, and a long client id or audience list wraps
// rather than pushing the last column off the card.
describe("the clients table", () => {
  it("keeps the row's size and wraps what is long", () => {
    render(
      <MCPAuthorization
        inspect={inspect({
          registeredClients: [MCP_SERVER, REGISTERED],
        } as never)}
      />,
    );
    const row = screen.getByRole("row", {
      name: new RegExp(REGISTERED.clientId),
    });
    const [client, , kind, scopes, audiences] =
      within(row).getAllByRole("cell");
    expect(kind).not.toHaveClass("text-xs");
    for (const cell of [client, scopes, audiences]) {
      expect(cell).toHaveClass("whitespace-normal");
    }
  });

  // A status is a column of its own: under an id the width of a UUID, its
  // badge wrapped beneath it and broke the row.
  it.each([
    ["a client the configuration seeds", MCP_SERVER, "configured"],
    ["a client that registered itself", REGISTERED, "registered itself"],
    [
      "a client dropped from the configuration",
      { ...REGISTERED, clientId: "old-agent", dynamic: false },
      "no longer configured",
    ],
    [
      "a client that skips consent",
      { ...MCP_SERVER, clientId: "console", skipConsent: true },
      "skips consent",
    ],
  ])("names %s in the Source column", (_, c, label) => {
    render(
      <MCPAuthorization
        inspect={inspect({ registeredClients: [c] } as never)}
      />,
    );
    const row = screen.getByRole("row", { name: new RegExp(c.clientId) });
    const [client, source] = within(row).getAllByRole("cell");
    expect(within(source).getByText(label)).toBeInTheDocument();
    expect(client).toHaveTextContent(new RegExp(`^${c.clientId}$`));
  });
});
