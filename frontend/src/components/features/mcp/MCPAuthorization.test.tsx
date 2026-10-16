import { describe, expect, it } from "vitest";
import { render, screen } from "@/test/utils";

import type { MCPInspectResponse } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";
import { MCPAuthorization } from "./MCPAuthorization";
import { MCPConnect } from "./MCPOverview";
import { EffectBadge } from "./MCPCatalog";

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
      screen.getByText("Accepted audience").nextElementSibling,
    ).toHaveTextContent("paladin-mcp");
    expect(screen.getAllByText(MCP_SERVER.clientId)).toHaveLength(2);
    expect(screen.getByText("MCP server")).toBeInTheDocument();
    expect(screen.getByText("public (PKCE)")).toBeInTheDocument();
  });

  it("says a forwarding server takes any plane's token", () => {
    render(<MCPAuthorization inspect={inspect({})} />);
    expect(
      screen.getByText("Accepted audience").nextElementSibling,
    ).toHaveTextContent("any plane's token");
    expect(
      screen.getByText("No client is registered at boot."),
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
