import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, within } from "@/test/utils";
import type { MCPInspectResponse } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";

import { MCPTools } from "./MCPCatalog";

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
