import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { create } from "@bufbuild/protobuf";

import {
  PreviewPurgeTenantResponseSchema,
  PurgeBlockerKind,
  type PreviewPurgeTenantResponse,
} from "@/gen/paladin/admin/v1/tenant_service_pb";

import {
  fireEvent,
  render,
  screen,
  typeToConfirm,
  waitFor,
  within,
} from "@/test/utils";

const DAY = 24 * 60 * 60 * 1000;
const RETENTION_DAYS = 30;

function trashedTenant(
  id: string,
  slug: string,
  displayName: string,
  daysAgo: number,
) {
  const deleted = new Date(Date.now() - daysAgo * DAY);
  return {
    tenantId: id,
    slug,
    displayName,
    deletedAt: timestampFromDate(deleted),
    purgeEligibleAt: timestampFromDate(
      new Date(deleted.getTime() + RETENTION_DAYS * DAY),
    ),
  };
}

const h = vi.hoisted(() => ({
  tenants: [] as unknown[],
  purgeTenant: vi.fn(),
  restoreTenant: vi.fn(),
  notify: vi.fn(),
  // The purge preview the confirmation reads: unread by default, so the
  // confirmation lists the kinds a purge removes and lets it go ahead.
  preview: {} as {
    data?: PreviewPurgeTenantResponse;
    isPending: boolean;
    isError: boolean;
    error?: Error;
  },
}));

vi.mock("@/hooks/usePurgePreview", () => ({
  usePurgePreview: (tenantId?: string) =>
    tenantId ? h.preview : { isPending: true, isError: false },
}));

vi.mock("@/hooks/useTenants", () => ({
  useTenants: () => ({
    tenants: h.tenants,
    loading: false,
    refetch: vi.fn(),
    fetchedAt: Date.now(),
    restoreTenant: h.restoreTenant,
    purgeTenant: h.purgeTenant,
    error: null,
  }),
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));

import TrashPage from "./page";

describe("TrashPage", () => {
  beforeEach(() => {
    h.purgeTenant.mockReset();
    h.restoreTenant.mockReset();
    h.notify.mockReset();
    h.preview = {
      isPending: false,
      isError: true,
      error: new Error("preview unavailable"),
    };
    h.tenants = [
      trashedTenant("t-new", "smoke-new", "Newer", 2),
      trashedTenant("t-old", "smoke-old", "Older", 40),
      trashedTenant("t-oldest", "smoke-oldest", "Oldest", 50),
    ];
  });

  const tenantColumn = () =>
    screen
      .getAllByRole("row")
      .slice(1)
      .map(
        (r) =>
          within(r).getAllByRole("cell")[0].querySelector("p")?.textContent,
      );

  it("narrows to the tenants past or within retention, counting each", async () => {
    render(<TrashPage />);
    await screen.findByText("Older");
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by retention" }),
    );
    const past = screen.getByRole("menuitemcheckbox", {
      name: /Past retention/,
    });
    expect(past).toHaveTextContent("2");
    await userEvent.click(past);
    await userEvent.keyboard("{Escape}");
    expect(tenantColumn()).toEqual(["Oldest", "Older"]);
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by retention, 1 chosen" }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /Past retention/ }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /Within retention/ }),
    );
    await userEvent.keyboard("{Escape}");
    expect(tenantColumn()).toEqual(["Newer"]);
    expect(screen.getByText("1 of 3")).toBeInTheDocument();
  });

  it("searches the slug and sorts by name", async () => {
    render(<TrashPage />);
    await screen.findByText("Older");
    fireEvent.change(
      screen.getByRole("searchbox", { name: "Search the trash" }),
      {
        target: { value: "smoke-old" },
      },
    );
    // Both slugs start so; the oldest still leads.
    expect(tenantColumn()).toEqual(["Oldest", "Older"]);
    fireEvent.change(
      screen.getByRole("searchbox", { name: "Search the trash" }),
      {
        target: { value: "" },
      },
    );
    fireEvent.click(screen.getByRole("button", { name: /^Tenant/ }));
    expect(tenantColumn()).toEqual(["Newer", "Older", "Oldest"]);
    fireEvent.change(
      screen.getByRole("searchbox", { name: "Search the trash" }),
      {
        target: { value: "absent" },
      },
    );
    expect(screen.getByText("No trashed tenant matches.")).toBeInTheDocument();
  });

  it("lists the oldest first and counts those past retention", async () => {
    render(<TrashPage />);
    await screen.findByText("Older");
    const rows = screen.getAllByRole("row").slice(1);
    expect(within(rows[0]).getByText("Oldest")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Older")).toBeInTheDocument();
    expect(within(rows[2]).getByText("Newer")).toBeInTheDocument();
    expect(screen.getByText(/3 tenants/)).toBeInTheDocument();
    expect(screen.getByText("2 past retention")).toBeInTheDocument();
    expect(screen.getByText(/30-day retention/)).toBeInTheDocument();
    expect(within(rows[0]).getByText("Past retention")).toBeInTheDocument();
    expect(within(rows[2]).getByText("28 days left")).toBeInTheDocument();
  });

  // The e2e suite finds a tenant by its slug, strictly: it must appear once.
  it("shows each slug once, beside its copy control", async () => {
    render(<TrashPage />);
    await screen.findByText("Older");
    expect(screen.getAllByText("smoke-old")).toHaveLength(1);
    expect(screen.getAllByRole("button", { name: "Copy Slug" })).toHaveLength(
      3,
    );
  });

  it("names a tenant with no display name by its slug, once", async () => {
    h.tenants = [trashedTenant("t-bare", "bare-slug", "", 1)];
    render(<TrashPage />);
    expect(await screen.findAllByText("bare-slug")).toHaveLength(1);
  });

  // Purge is irreversible and cascades, so it asks for the slug, as Argo CD
  // asks for an application's name, and names what goes with the tenant.
  const confirmPurge = async (slug: string) => {
    await typeToConfirm(slug);
    fireEvent.click(screen.getByRole("button", { name: "Purge permanently" }));
  };

  it("purges only once the slug is typed, and lists what goes with it", async () => {
    h.purgeTenant.mockResolvedValue(undefined);
    render(<TrashPage />);
    await screen.findByText("Older");
    fireEvent.click(screen.getByRole("button", { name: "Purge smoke-old" }));
    const dialog = await screen.findByRole("alertdialog");
    const cascade = within(dialog).getByRole("list", {
      name: "Removed with it",
    });
    expect(cascade).toHaveTextContent("API tokens");
    expect(cascade).toHaveTextContent("Event subscriptions");
    expect(dialog).toHaveTextContent("could not be read");
    const purge = within(dialog).getByRole("button", {
      name: "Purge permanently",
    });
    expect(purge).toBeDisabled();
    fireEvent.change(
      within(dialog).getByRole("textbox", { name: /to confirm/ }),
      { target: { value: "smoke-oldest" } },
    );
    expect(purge).toBeDisabled();
    await confirmPurge("smoke-old");
    await waitFor(() => expect(h.purgeTenant).toHaveBeenCalledWith("t-old"));
  });

  // The confirmation names what this tenant holds, not just the kinds.
  it("names the tenant's tokens and subscriptions from the preview", async () => {
    h.preview = {
      isPending: false,
      isError: false,
      data: create(PreviewPurgeTenantResponseSchema, {
        apiTokens: [{ name: "tenants/t-old/apiTokens/1", displayName: "ci" }],
        apiTokenCount: 3n,
        eventSubscriptions: [
          { name: "tenants/t-old/eventSubscriptions/2", displayName: "http" },
        ],
        eventSubscriptionCount: 1n,
        capabilityCount: 4n,
      }),
    };
    h.purgeTenant.mockResolvedValue(undefined);
    render(<TrashPage />);
    await screen.findByText("Older");
    fireEvent.click(screen.getByRole("button", { name: "Purge smoke-old" }));
    const dialog = await screen.findByRole("alertdialog");
    const tokens = within(dialog).getByRole("list", {
      name: "3 API tokens: integrations using them stop at once",
    });
    expect(tokens).toHaveTextContent("ci");
    expect(tokens).toHaveTextContent("and 2 more");
    expect(
      within(dialog).getByRole("list", { name: "Also removed" }),
    ).toHaveTextContent("4 capabilities");
    expect(within(dialog).queryByText(/could not be read/)).toBeNull();
    await confirmPurge("smoke-old");
    await waitFor(() => expect(h.purgeTenant).toHaveBeenCalledWith("t-old"));
  });

  // A purge the database would refuse is not offered: the dialog says what
  // to remove first.
  it("refuses a purge the tenant's rows would block, and says why", async () => {
    h.preview = {
      isPending: false,
      isError: false,
      data: create(PreviewPurgeTenantResponseSchema, {
        blockers: [{ kind: PurgeBlockerKind.USERS, count: 2n }],
      }),
    };
    render(<TrashPage />);
    await screen.findByText("Older");
    fireEvent.click(screen.getByRole("button", { name: "Purge smoke-old" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("still owns 2 users");
    fireEvent.change(
      within(dialog).getByRole("textbox", { name: /to confirm/ }),
      { target: { value: "smoke-old" } },
    );
    expect(
      within(dialog).getByRole("button", { name: "Purge permanently" }),
    ).toBeDisabled();
  });

  it("waits for the preview before offering the purge", async () => {
    h.preview = { isPending: true, isError: false };
    render(<TrashPage />);
    await screen.findByText("Older");
    fireEvent.click(screen.getByRole("button", { name: "Purge smoke-old" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.change(
      within(dialog).getByRole("textbox", { name: /to confirm/ }),
      { target: { value: "smoke-old" } },
    );
    expect(
      within(dialog).getByRole("button", { name: "Purge permanently" }),
    ).toBeDisabled();
  });

  // A refused purge is reported under the tenant's own row, not in a toast
  // that disappears.
  it("keeps a refused purge's reason under its row", async () => {
    h.purgeTenant.mockRejectedValue(
      new Error("tenant still has rows referencing it: charges"),
    );
    render(<TrashPage />);
    await screen.findByText("Older");
    fireEvent.click(screen.getByRole("button", { name: "Purge smoke-old" }));
    await confirmPurge("smoke-old");

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Purge refused.");
    expect(alert).toHaveTextContent("charges");
    expect(h.purgeTenant).toHaveBeenCalledWith("t-old");
    expect(h.notify).not.toHaveBeenCalled();
  });

  it("clears the reason once the purge goes through", async () => {
    h.purgeTenant
      .mockRejectedValueOnce(
        new Error("tenant still has rows referencing it: charges"),
      )
      .mockResolvedValueOnce(undefined);
    render(<TrashPage />);
    await screen.findByText("Older");
    for (let i = 0; i < 2; i++) {
      fireEvent.click(screen.getByRole("button", { name: "Purge smoke-old" }));
      await confirmPurge("smoke-old");
      await waitFor(() => expect(h.purgeTenant).toHaveBeenCalledTimes(i + 1));
    }
    await waitFor(() =>
      expect(screen.queryByRole("alert")).not.toBeInTheDocument(),
    );
    expect(h.notify).toHaveBeenCalledWith(
      expect.objectContaining({ type: "success", title: "Tenant purged" }),
    );
  });

  it("asks before restoring, and restores only once confirmed", async () => {
    h.restoreTenant.mockResolvedValue({ tenantId: "t-old", slug: "smoke-old" });
    render(<TrashPage />);
    await screen.findByText("Older");
    const row = screen
      .getAllByRole("row")
      .find((r) => within(r).queryByText("Older"))!;
    fireEvent.click(within(row).getByRole("button", { name: /Restore/ }));

    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("smoke-old");
    expect(within(dialog).getByRole("note")).toHaveTextContent(/slug/);
    expect(h.restoreTenant).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() =>
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument(),
    );
    expect(h.restoreTenant).not.toHaveBeenCalled();

    fireEvent.click(within(row).getByRole("button", { name: /Restore/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Restore tenant" }),
    );
    await waitFor(() => expect(h.restoreTenant).toHaveBeenCalledWith("t-old"));
    expect(h.restoreTenant).toHaveBeenCalledOnce();
  });

  it("does not purge when the confirmation is cancelled", async () => {
    render(<TrashPage />);
    await screen.findByText("Older");
    fireEvent.click(screen.getByRole("button", { name: "Purge smoke-old" }));
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    await waitFor(() =>
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument(),
    );
    expect(h.purgeTenant).not.toHaveBeenCalled();
  });

  it("says the trash is empty", async () => {
    h.tenants = [];
    render(<TrashPage />);
    expect(await screen.findByText("Trash is empty.")).toBeInTheDocument();
  });
});
