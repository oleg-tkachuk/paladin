import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@/test/utils";

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
    expect(screen.getAllByText("registered itself")).toHaveLength(1);
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

    fireEvent.click(
      within(confirm).getByRole("button", { name: "Delete client" }),
    );
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
