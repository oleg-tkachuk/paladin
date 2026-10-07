import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

import { ScopeType } from "@/gen/paladin/common/v1/scope_pb";

const h = vi.hoisted(() => ({
  getUser: vi.fn(),
  getTenant: vi.fn(),
  deleteUser: vi.fn(),
  push: vi.fn(),
}));

vi.mock("@/lib/connect/client", () => ({
  userClient: { getUser: h.getUser },
  tenantClient: { getTenant: h.getTenant },
}));
vi.mock("next/navigation", () => ({
  useParams: () => ({ tenant: "t-1", user: "u-1" }),
  useRouter: () => ({ push: h.push }),
  usePathname: () => "/users/t-1/u-1",
}));
vi.mock("@/hooks/useUserAdmin", () => ({
  useUserAdmin: () => ({
    busy: false,
    updateUser: vi.fn(),
    deleteUser: h.deleteUser,
    resetPassword: vi.fn(),
    grantScopes: vi.fn(),
    revokeScopes: vi.fn(),
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({
    title,
    actions,
  }: {
    title: React.ReactNode;
    actions?: React.ReactNode;
  }) => (
    <div>
      <h1>{title}</h1>
      {actions}
    </div>
  ),
}));

import UserDetailPage from "./page";
import { CrumbNamesProvider, useCrumbNames } from "@/context/CrumbNamesContext";

function Trail() {
  const names = useCrumbNames();
  return (
    <p data-testid="trail">
      {["t-1", "u-1"].map((s) => names[s]?.label ?? s).join(" / ")}
    </p>
  );
}

const USER = {
  name: "tenants/t-1/users/u-1",
  userId: "u-1",
  tenantId: "t-1",
  subject: "alice",
  displayName: "Alice",
  roles: ["tenant.user"],
  scopes: [{ type: ScopeType.BUCKET, value: "media" }],
  disabled: false,
  resourceVersion: "2",
};

beforeEach(() => {
  h.getUser.mockReset().mockResolvedValue(USER);
  h.getTenant.mockReset().mockResolvedValue({ slug: "acme" });
  h.deleteUser.mockReset().mockResolvedValue({ ok: true });
  h.push.mockReset();
});

// There was no page for one user: roles and scopes could not be read or
// changed from the console.
describe("UserDetailPage", () => {
  it("reads the user the path names and shows roles and scopes", async () => {
    render(<UserDetailPage />);
    expect(
      await screen.findByRole("heading", { name: "Alice" }),
    ).toBeInTheDocument();
    expect(h.getUser).toHaveBeenCalledWith(
      { name: "tenants/t-1/users/u-1" },
      expect.anything(),
    );
    expect(screen.getByText("bucket:media")).toBeInTheDocument();
    expect(screen.getByLabelText(/tenant\.user/)).toBeChecked();
  });

  it("says the user could not be read", async () => {
    h.getUser.mockRejectedValue(new Error("not found"));
    render(<UserDetailPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(/not found/);
  });

  it("returns to the list after a delete", async () => {
    render(<UserDetailPage />);
    await userEvent.click(
      await screen.findByRole("button", { name: "Actions for alice" }),
    );
    await userEvent.click(screen.getByText("Delete"));
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(h.push).toHaveBeenCalledWith("/users"));
  });

  // The trail and the tab title showed the path's ids.
  it("names the tenant by slug and the user by login in the trail", async () => {
    render(
      <CrumbNamesProvider>
        <UserDetailPage />
        <Trail />
      </CrumbNamesProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("trail")).toHaveTextContent("acme / alice"),
    );
    expect(h.getTenant).toHaveBeenCalledWith(
      { name: "tenants/t-1" },
      expect.anything(),
    );
  });
});
