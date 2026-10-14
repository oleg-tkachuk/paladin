import { beforeEach, describe, expect, it, vi } from "vitest";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { fireEvent, render, screen, waitFor, within } from "@/test/utils";

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
}));

vi.mock("@/hooks/useTenants", () => ({
  useTenants: () => ({
    fetchTenants: () => Promise.resolve({ tenants: h.tenants }),
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
    h.tenants = [
      trashedTenant("t-new", "smoke-new", "Newer", 2),
      trashedTenant("t-old", "smoke-old", "Older", 40),
      trashedTenant("t-oldest", "smoke-oldest", "Oldest", 50),
    ];
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

  // A refused purge is reported under the tenant's own row, not in a toast
  // that disappears.
  it("keeps a refused purge's reason under its row", async () => {
    h.purgeTenant.mockRejectedValue(
      new Error("tenant still has rows referencing it: charges"),
    );
    render(<TrashPage />);
    await screen.findByText("Older");
    fireEvent.click(screen.getByRole("button", { name: "Purge smoke-old" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Purge permanently" }),
    );

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
      fireEvent.click(
        await screen.findByRole("button", { name: "Purge permanently" }),
      );
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
