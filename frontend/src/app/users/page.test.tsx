import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({ listUsers: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  userClient: { listUsers: h.listUsers },
}));

const user = (id: string, name: string) => ({
  name: `tenants/t-1/users/${id}`,
  userId: id,
  tenantId: "t-1",
  subject: name.toLowerCase(),
  displayName: name,
  roles: [],
  scopes: [],
  disabled: false,
  resourceVersion: "1",
});

vi.mock("@/hooks/useTenants", () => ({
  useTenants: () => ({ tenants: [], error: null, fetchTenants: vi.fn() }),
}));
vi.mock("@/hooks/useUserAdmin", () => ({
  useUserAdmin: () => ({ busy: false }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: () => null,
}));

import UsersPage from "./page";

describe("UsersPage", () => {
  beforeEach(() => {
    h.listUsers.mockReset();
    h.listUsers.mockResolvedValue({ users: [user("u-1", "Alice")], page: {} });
  });

  // The search runs over the loaded rows; a user past the first page was
  // neither listed nor found.
  it("lists the users of every page", async () => {
    h.listUsers
      .mockResolvedValueOnce({
        users: [user("u-1", "Alice")],
        page: { nextPageToken: "p2" },
      })
      .mockResolvedValueOnce({ users: [user("u-2", "Bob")], page: {} });
    render(<UsersPage />);
    expect(
      await screen.findByRole("link", { name: "Bob" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Alice" })).toBeInTheDocument();
  });

  it("narrows to the disabled users, and sorts by name", async () => {
    h.listUsers.mockResolvedValue({
      users: [
        user("u-2", "Carol"),
        { ...user("u-3", "Bob"), disabled: true },
        user("u-1", "Alice"),
      ],
      page: {},
    });
    render(<UsersPage />);
    await screen.findByRole("link", { name: "Carol" });
    const names = () =>
      screen
        .getAllByRole("link")
        .map((l) => l.textContent)
        .filter((t) => ["Alice", "Bob", "Carol"].includes(t ?? ""));

    fireEvent.click(screen.getByRole("button", { name: "Disabled" }));
    expect(names()).toEqual(["Bob"]);
    expect(screen.getByText("1 of 3")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "All" }));
    fireEvent.click(screen.getByRole("button", { name: /^User/ }));
    expect(names()).toEqual(["Alice", "Bob", "Carol"]);
  });

  // The list's view lives in the URL: a reload or a shared link opens it as
  // it was left, and changing it writes it back.
  it("opens with the filter, search and sort a link names", async () => {
    h.listUsers.mockResolvedValue({
      users: [
        user("u-2", "Carol"),
        { ...user("u-4", "Bert"), disabled: true },
        { ...user("u-5", "Dana"), disabled: true },
        { ...user("u-3", "Bob"), disabled: true },
        user("u-1", "Alice"),
      ],
      page: {},
    });
    window.history.replaceState(
      null,
      "",
      "/users?filter=disabled&q=b&sort=user&dir=desc",
    );
    render(<UsersPage />);
    await screen.findByRole("link", { name: "Bob" });
    expect(
      screen
        .getAllByRole("link")
        .map((l) => l.textContent)
        .filter((t) =>
          ["Alice", "Bert", "Bob", "Carol", "Dana"].includes(t ?? ""),
        ),
    ).toEqual(["Bob", "Bert"]);
    expect(screen.getByRole("button", { name: "Disabled" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    fireEvent.click(screen.getByRole("button", { name: "All" }));
    expect(window.location.search).toBe("?q=b&sort=user&dir=desc");
  });

  it("opens each user on a page of their own", async () => {
    render(<UsersPage />);
    expect(await screen.findByRole("link", { name: "Alice" })).toHaveAttribute(
      "href",
      "/users/t-1/u-1",
    );
  });
});
