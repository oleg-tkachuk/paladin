import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

vi.mock("@/lib/connect/client", () => ({
  userClient: {
    listUsers: vi.fn().mockResolvedValue({
      users: [
        {
          name: "tenants/t-1/users/u-1",
          userId: "u-1",
          tenantId: "t-1",
          subject: "alice",
          displayName: "Alice",
          roles: [],
          scopes: [],
          disabled: false,
          resourceVersion: "1",
        },
      ],
    }),
  },
}));
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
  it("opens each user on a page of their own", async () => {
    render(<UsersPage />);
    expect(await screen.findByRole("link", { name: "Alice" })).toHaveAttribute(
      "href",
      "/users/t-1/u-1",
    );
  });
});
