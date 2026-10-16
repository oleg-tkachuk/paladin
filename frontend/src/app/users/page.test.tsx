import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

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

  it("opens each user on a page of their own", async () => {
    render(<UsersPage />);
    expect(await screen.findByRole("link", { name: "Alice" })).toHaveAttribute(
      "href",
      "/users/t-1/u-1",
    );
  });
});
