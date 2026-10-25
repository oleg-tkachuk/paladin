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
  clearRecent: vi.fn(async () => {}),
  unpinAll: vi.fn(async () => {}),
  setShown: vi.fn(async () => {}),
  shown: true,
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
    clearRecent: h.clearRecent,
    unpinAll: h.unpinAll,
    setShown: h.setShown,
    shown: h.shown,
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
  h.shown = true;
  h.clearRecent.mockClear();
  h.unpinAll.mockClear();
  h.setShown.mockClear();
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

  // A long name shrinks to its ellipsis, so the kind beside it and the
  // section's options stay in view: a flex item does not shrink below its
  // text without min-w-0.
  it("lets a long name give way to the kind and the options", () => {
    const long = {
      href: "/tenants/acme/collections/docs/objects/jgjvugm2qj2ulhgbqqnpmrneaa",
      label: "jgjvugm2qj2ulhgbqqnpmrneaa-and-then-some-more",
      kind: "Object",
    };
    h.recent = [long];
    render(<SidebarResourceLinks groupLabelClass="" />);
    const name = screen.getByText(long.label);
    expect(name.className).toMatch(/\bmin-w-0\b/);
    expect(name.className).toMatch(/\btruncate\b/);
    expect(name.className).toMatch(/\bflex-1\b/);
    expect(screen.getByText(long.kind).className).toMatch(/\bshrink-0\b/);
    expect(
      screen.getByRole("button", { name: "Pinned and recent options" }),
    ).toBeInTheDocument();
  });
});

describe("SidebarResourceLinks options", () => {
  it.each([
    ["Clear recent", () => h.clearRecent],
    ["Unpin all", () => h.unpinAll],
  ])("%s from the section's menu", async (item, fn) => {
    h.pinned = [docs];
    h.recent = [acme];
    render(<SidebarResourceLinks groupLabelClass="" />);
    await userEvent.click(
      screen.getByRole("button", { name: "Pinned and recent options" }),
    );
    await userEvent.click(await screen.findByRole("menuitem", { name: item }));
    expect(fn()).toHaveBeenCalled();
  });

  it("hides the section from its menu, and is gone while off", async () => {
    h.recent = [acme];
    const { rerender } = render(<SidebarResourceLinks groupLabelClass="" />);
    await userEvent.click(
      screen.getByRole("button", { name: "Pinned and recent options" }),
    );
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Hide this section" }),
    );
    expect(h.setShown).toHaveBeenCalledWith(false);
    h.shown = false;
    rerender(<SidebarResourceLinks groupLabelClass="" />);
    expect(screen.queryByTestId("resource-links")).toBeNull();
  });

  it("offers no clear when there is nothing to clear", async () => {
    h.pinned = [docs];
    render(<SidebarResourceLinks groupLabelClass="" />);
    await userEvent.click(
      screen.getByRole("button", { name: "Pinned and recent options" }),
    );
    expect(
      await screen.findByRole("menuitem", { name: "Clear recent" }),
    ).toHaveAttribute("aria-disabled", "true");
  });
});
