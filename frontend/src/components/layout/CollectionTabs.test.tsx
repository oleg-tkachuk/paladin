import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({
  path: "/tenants/acme/collections/docs/objects",
}));
vi.mock("next/navigation", () => ({ usePathname: () => h.path }));

import { CollectionTabs } from "./CollectionTabs";

describe("CollectionTabs", () => {
  it("links each tab under the collection and marks the open one", () => {
    render(<CollectionTabs tenantId="acme" collection="docs" />);
    const objects = screen.getByRole("tab", { name: "Objects" });
    expect(objects).toHaveAttribute(
      "href",
      "/tenants/acme/collections/docs/objects",
    );
    expect(objects).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute(
      "aria-selected",
      "false",
    );
  });

  // In the collection's one-row header the row draws the rule, so the strip
  // takes the row's classes in place of its own full-width one.
  it("takes the classes of the row it sits in", () => {
    render(
      <CollectionTabs
        tenantId="acme"
        collection="docs"
        className="border-b-0"
      />,
    );
    expect(screen.getByRole("tablist")).toHaveClass("border-b-0");
  });
});
