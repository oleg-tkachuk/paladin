import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({
  pathname: "/",
  names: {} as Record<string, { label: string }>,
  pinned: [] as { href: string; label: string; kind: string }[],
  recent: [] as { href: string; label: string; kind: string }[],
  visit: vi.fn(),
  togglePin: vi.fn(async () => {}),
}));
vi.mock("next/navigation", async (importOriginal) => ({
  ...(await importOriginal<typeof import("next/navigation")>()),
  usePathname: () => h.pathname,
}));
vi.mock("@/context/CrumbNamesContext", () => ({
  useCrumbNames: () => h.names,
}));
vi.mock("@/hooks/useResourceLinks", () => ({
  useResourceLinks: () => ({
    pinned: h.pinned,
    recent: h.recent,
    visit: h.visit,
    togglePin: h.togglePin,
    isPinned: (href: string) => h.pinned.some((p) => p.href === href),
  }),
}));

import { PinToggle, RecordVisits, SidebarResourceLinks } from "./ResourceLinks";

const acme = { href: "/tenants/acme", label: "Acme", kind: "Tenant" };
const docs = {
  href: "/tenants/acme/collections/docs",
  label: "docs",
  kind: "Collection",
};

beforeEach(() => {
  h.pathname = "/";
  h.names = {};
  h.pinned = [];
  h.recent = [];
  h.visit.mockReset();
  h.togglePin.mockClear();
});

describe("RecordVisits", () => {
  it("remembers a resource page by the name the page gave its id", () => {
    h.pathname = "/tenants/acme";
    h.names = { acme: { label: "Acme" } };
    render(<RecordVisits />);
    expect(h.visit).toHaveBeenCalledWith(acme);
  });

  it("remembers nothing on a page that shows no one resource", () => {
    h.pathname = "/tenants";
    render(<RecordVisits />);
    expect(h.visit).not.toHaveBeenCalled();
  });
});

describe("PinToggle", () => {
  it("pins the resource on screen, and says when it is pinned", async () => {
    h.pathname = "/tenants/acme";
    h.names = { acme: { label: "Acme" } };
    const { rerender } = render(<PinToggle />);
    await userEvent.click(screen.getByRole("button", { name: "Pin Acme" }));
    expect(h.togglePin).toHaveBeenCalledWith(acme);
    h.pinned = [acme];
    rerender(<PinToggle />);
    expect(screen.getByRole("button", { name: "Unpin Acme" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("is not offered off a resource page", () => {
    h.pathname = "/operations";
    render(<PinToggle />);
    expect(screen.queryByRole("button")).toBeNull();
  });
});

describe("SidebarResourceLinks", () => {
  it("lists the pinned, then the recent, each once", () => {
    h.pinned = [docs];
    h.recent = [acme, docs];
    render(<SidebarResourceLinks groupLabelClass="" />);
    expect(
      screen.getAllByRole("link").map((a) => a.getAttribute("href")),
    ).toEqual([docs.href, acme.href]);
  });

  it("takes no room when there is nothing to list", () => {
    render(<SidebarResourceLinks groupLabelClass="" />);
    expect(screen.queryByTestId("resource-links")).toBeNull();
  });
});
