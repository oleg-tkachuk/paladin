import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

// next/navigation is mocked with a mutable pathname so each test can drive a
// different route through the breadcrumb resolver.
let pathname = "/";
let search = "";
vi.mock("next/navigation", () => ({
  usePathname: () => pathname,
  useSearchParams: () => new URLSearchParams(search),
}));

import { Breadcrumbs } from "./Breadcrumbs";

beforeEach(() => {
  pathname = "/";
  search = "";
});

describe("Breadcrumbs", () => {
  it("renders nothing at the root", () => {
    pathname = "/";
    const { container } = render(<Breadcrumbs />);
    expect(container.querySelector("nav")).toBeNull();
  });

  it("renders Home plus one crumb per path segment", () => {
    pathname = "/storage-backends";
    render(<Breadcrumbs />);
    expect(
      screen.getByRole("navigation", { name: "Breadcrumb" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Home")).toBeInTheDocument(); // sr-only label
    // "storage-backends" → "Storage backends" (dash-replaced, capitalized).
    expect(screen.getByText("Storage backends")).toBeInTheDocument();
  });

  it.each([
    ["/mcp", "MCP"],
    ["/tenants/platform/m2m-tokens", "M2M tokens"],
  ])("keeps the acronym in %s", (path, label) => {
    pathname = path;
    render(<Breadcrumbs />);
    expect(screen.getByText(label)).toBeInTheDocument();
  });

  it("marks the last segment aria-current=page and leaves earlier ones linked", () => {
    pathname = "/tenants/abc-123/buckets";
    render(<Breadcrumbs />);
    const current = screen.getByText("Buckets");
    expect(current).toHaveAttribute("aria-current", "page");
    // The "tenants" crumb (not last) is a live link to /tenants.
    const tenantsLink = screen.getByRole("link", { name: "Tenants" });
    expect(tenantsLink).toHaveAttribute("href", "/tenants");
  });

  it("decodes an entity-id segment to its short name (child of an entity parent)", () => {
    // "objects/<key>" — the key is decoded + last-segment-shortened.
    pathname =
      "/tenants/t1/collections/k1/objects/" +
      encodeURIComponent("a/b/file.txt");
    render(<Breadcrumbs />);
    expect(screen.getByText("file.txt")).toBeInTheDocument();
  });
});
