import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, within } from "@/test/utils";
import type { MCPInspectResponse } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";

import { MCPAlwaysDeny, MCPProfiles, MCPTools } from "./MCPCatalog";

const tool = (
  name: string,
  effect: { mutates?: boolean; destructive?: boolean },
  capabilityOp = "",
) => ({
  name,
  audience: "data",
  mutates: !!effect.mutates || !!effect.destructive,
  destructive: !!effect.destructive,
  openWorld: false,
  description: `${name} does a thing`,
  capabilityOp,
});

const INSPECT = {
  toolCatalog: [
    tool("paladin_delete_object", { destructive: true }, "delete"),
    tool("paladin_list_objects", {}, "list"),
    tool("paladin_update_object", { mutates: true }),
  ],
  profiles: [],
  alwaysDeny: [],
  transports: {},
} as unknown as MCPInspectResponse;

const toolColumn = () =>
  within(screen.getByRole("table"))
    .getAllByRole("row")
    .slice(1)
    .map((r) => within(r).getAllByRole("cell")[0].textContent);

const TOOLS_PATH = "/mcp/tools";
beforeEach(() => {
  window.history.replaceState(null, "", TOOLS_PATH);
});

describe("MCPTools", () => {
  it("sorts by effect, reads first, and back", () => {
    render(<MCPTools inspect={INSPECT} />);
    const effect = screen.getByRole("button", { name: /^Effect/ });
    fireEvent.click(effect);
    expect(toolColumn()).toEqual([
      "paladin_list_objects",
      "paladin_update_object",
      "paladin_delete_object",
    ]);
    fireEvent.click(effect);
    expect(toolColumn()).toEqual([
      "paladin_delete_object",
      "paladin_update_object",
      "paladin_list_objects",
    ]);
  });

  it("sorts tools without a capability op last", () => {
    render(<MCPTools inspect={INSPECT} />);
    fireEvent.click(screen.getByRole("button", { name: /^Capability op/ }));
    expect(toolColumn()).toEqual([
      "paladin_delete_object",
      "paladin_list_objects",
      "paladin_update_object",
    ]);
  });

  it("searches the capability op as well as the name", () => {
    render(<MCPTools inspect={INSPECT} />);
    fireEvent.change(screen.getByRole("searchbox", { name: "Search tools" }), {
      target: { value: "list" },
    });
    expect(toolColumn()).toEqual(["paladin_list_objects"]);
    expect(screen.getByText("1 of 3")).toBeInTheDocument();
  });
});

// The catalog, the profiles and the always-deny list share /mcp/tools, and
// two of them open tools: each keeps its open record under its own name.
describe("the tools page's panels", () => {
  const WITH_PROFILES = {
    ...INSPECT,
    profiles: [
      {
        name: "read_only",
        source: "builtin",
        rawPatterns: ["paladin_list_*"],
        deny: [],
        tools: ["paladin_list_objects"],
      },
    ],
    alwaysDeny: ["paladin_delete_object"],
  } as unknown as MCPInspectResponse;

  it("opens a tool under the catalog's id", async () => {
    render(<MCPTools inspect={INSPECT} />);
    fireEvent.click(screen.getByRole("row", { name: /paladin_list_objects/ }));
    const panel = await screen.findByRole("dialog", { name: "Tool" });
    expect(panel).toHaveAccessibleDescription("paladin_list_objects");
    expect(window.location.search).toBe("?tools.id=paladin_list_objects");
  });

  it("opens a profile from a link", async () => {
    window.history.replaceState(
      null,
      "",
      `${TOOLS_PATH}?profiles.id=read_only`,
    );
    render(<MCPProfiles inspect={WITH_PROFILES} />);
    const panel = await screen.findByRole("dialog", { name: "Profile" });
    expect(panel).toHaveTextContent("Tools (1 of 3)");
  });

  it("opens a denied tool under the deny list's id, not the catalog's", async () => {
    render(
      <>
        <MCPTools inspect={WITH_PROFILES} />
        <MCPAlwaysDeny inspect={WITH_PROFILES} />
      </>,
    );
    const denied = screen
      .getAllByRole("row", { name: /paladin_delete_object/ })
      .at(-1)!;
    fireEvent.click(denied);
    const panels = await screen.findAllByRole("dialog");
    expect(panels).toHaveLength(1);
    expect(panels[0]).toHaveTextContent("always denied");
    expect(window.location.search).toBe("?denied.id=paladin_delete_object");
  });

  it("opens only the catalog's panel for the catalog's id", async () => {
    window.history.replaceState(
      null,
      "",
      `${TOOLS_PATH}?tools.id=paladin_delete_object`,
    );
    render(
      <>
        <MCPTools inspect={WITH_PROFILES} />
        <MCPProfiles inspect={WITH_PROFILES} />
        <MCPAlwaysDeny inspect={WITH_PROFILES} />
      </>,
    );
    expect(await screen.findAllByRole("dialog")).toHaveLength(1);
    expect(screen.getByRole("dialog", { name: "Tool" })).toBeInTheDocument();
  });
});
