import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@/test/utils";

import type { MCPInspectResponse } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";
import { MCPAuthorization } from "./MCPAuthorization";
import { MCPConnect } from "./MCPOverview";
import { EffectBadge } from "./MCPCatalog";

vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

const AGENT = {
  clientId: "paladin-e2e-agent",
  public: true,
  allowedScopes: ["paladin.read"],
  allowedAudiences: ["paladin-mcp"],
  tokenExchange: false,
  skipConsent: false,
};
const MCP_SERVER = {
  clientId: "paladin-mcp-server",
  public: false,
  allowedScopes: [],
  allowedAudiences: [],
  tokenExchange: true,
  skipConsent: false,
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
      screen.getByRole("button", { name: `Open client ${AGENT.clientId}` }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("by the configuration");
    expect(dialog).toHaveTextContent("paladin.read");
    expect(dialog).toHaveTextContent("asked");
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
