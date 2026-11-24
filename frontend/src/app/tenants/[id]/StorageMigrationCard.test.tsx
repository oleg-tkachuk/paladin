import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// StorageMigrationCard drives the tenant client through useTenants; mock the
// client. getTenantStorageMigration → null models "no migration yet".
const h = vi.hoisted(() => ({
  getMig: vi.fn(),
  migrate: vi.fn(),
  cancel: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  tenantClient: {
    getTenantStorageMigration: h.getMig,
    migrateTenantStorageLayout: h.migrate,
    cancelTenantStorageMigration: h.cancel,
  },
}));

import { StorageMigrationCard } from "./StorageMigrationCard";

describe("StorageMigrationCard", () => {
  beforeEach(() => {
    h.getMig.mockReset();
    h.migrate.mockReset();
    h.cancel.mockReset();
  });

  it("offers a migrate trigger for a shared tenant with no migration, and calls the RPC", async () => {
    h.getMig.mockResolvedValue(null);
    h.migrate.mockResolvedValue({
      $typeName: "paladin.admin.v1.StorageMigrationStatus",
      state: "provisioning",
      objectsTotal: 0n,
      objectsCopied: 0n,
    });

    render(<StorageMigrationCard tenantId="t-1" storageLayout="shared" />);

    const btn = await screen.findByRole("button", {
      name: /migrate to dedicated/i,
    });
    await userEvent.click(btn);

    await waitFor(() => expect(h.migrate).toHaveBeenCalledTimes(1));
    expect(h.migrate).toHaveBeenCalledWith(
      expect.objectContaining({ name: "tenants/t-1" }),
    );
  });

  it("passes a custom cleanup retention (hours → seconds)", async () => {
    h.getMig.mockResolvedValue(null);
    h.migrate.mockResolvedValue({
      $typeName: "paladin.admin.v1.StorageMigrationStatus",
      state: "provisioning",
      objectsTotal: 0n,
      objectsCopied: 0n,
    });

    render(<StorageMigrationCard tenantId="t-1" storageLayout="shared" />);
    await screen.findByRole("button", { name: /migrate to dedicated/i });

    await userEvent.type(screen.getByPlaceholderText("24"), "48");
    await userEvent.click(
      screen.getByRole("button", { name: /migrate to dedicated/i }),
    );

    await waitFor(() => expect(h.migrate).toHaveBeenCalledTimes(1));
    // 48h → 172800s
    expect(h.migrate).toHaveBeenCalledWith(
      expect.objectContaining({ cleanupRetentionSeconds: 172800n }),
    );
  });

  it("shows a rollback after a failed verify as in progress, with its error", async () => {
    h.getMig.mockResolvedValue({
      $typeName: "paladin.admin.v1.StorageMigrationStatus",
      state: "rolling_back",
      objectsTotal: 2n,
      objectsCopied: 2n,
      sourceBucket: "storageBackends/primary/buckets/shared",
      targetBucket: "storageBackends/primary/buckets/paladin-t-1",
      error: "physical verify: docs/a size mismatch",
    });

    render(<StorageMigrationCard tenantId="t-1" storageLayout="dedicated" />);

    expect(await screen.findByText("rolling_back")).toBeInTheDocument();
    expect(
      screen.getByText(/being moved back to its source bucket/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText("physical verify: docs/a size mismatch"),
    ).toBeInTheDocument();
  });

  it("renders no migrate trigger for a dedicated tenant with no migration", async () => {
    h.getMig.mockResolvedValue(null);
    render(<StorageMigrationCard tenantId="t-1" storageLayout="dedicated" />);
    await waitFor(() => expect(h.getMig).toHaveBeenCalled());
    expect(
      screen.queryByRole("button", { name: /migrate to dedicated/i }),
    ).toBeNull();
  });

  const status = (state: string, resourceVersion = "7") => ({
    $typeName: "paladin.admin.v1.StorageMigrationStatus",
    state,
    objectsTotal: 2n,
    objectsCopied: 1n,
    sourceBucket: "storageBackends/primary/buckets/shared",
    targetBucket: "storageBackends/primary/buckets/paladin-t-1",
    error: "",
    resourceVersion,
  });

  it("cancels a moving migration at the version read, after a confirmation that warns", async () => {
    const cancelled = {
      ...status("rolling_back", "8"),
      error: "cancelled by u1",
    };
    h.getMig
      .mockResolvedValueOnce(status("moving"))
      .mockResolvedValue(cancelled);
    h.cancel.mockResolvedValue(cancelled);

    render(<StorageMigrationCard tenantId="t-1" storageLayout="dedicated" />);
    await userEvent.click(
      await screen.findByRole("button", { name: /cancel migration/i }),
    );
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent(/goes back to the shared bucket/i);
    expect(h.cancel).not.toHaveBeenCalled();

    await userEvent.click(
      within(dialog).getByRole("button", { name: /cancel migration/i }),
    );
    await waitFor(() => expect(h.cancel).toHaveBeenCalledTimes(1));
    expect(h.cancel).toHaveBeenCalledWith({
      name: "tenants/t-1",
      resourceVersion: "7",
    });
    expect(await screen.findByText("rolling_back")).toBeInTheDocument();
    expect(screen.getByText("cancelled by u1")).toBeInTheDocument();
  });

  it("shows a refused cancel in the confirmation", async () => {
    h.getMig.mockResolvedValue(status("completed"));
    h.cancel.mockRejectedValue(new Error("resource_version mismatch"));

    render(<StorageMigrationCard tenantId="t-1" storageLayout="dedicated" />);
    await userEvent.click(
      await screen.findByRole("button", { name: /cancel migration/i }),
    );
    const dialog = await screen.findByRole("alertdialog");
    await userEvent.click(
      within(dialog).getByRole("button", { name: /cancel migration/i }),
    );
    expect(
      await within(dialog).findByText(/resource_version mismatch/),
    ).toBeInTheDocument();
  });

  it.each(["rolling_back", "failed", "cleaned"])(
    "offers no cancel for a %s migration",
    async (state) => {
      h.getMig.mockResolvedValue(status(state));
      render(<StorageMigrationCard tenantId="t-1" storageLayout="dedicated" />);
      expect(await screen.findByText(state)).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: /cancel migration/i }),
      ).toBeNull();
    },
  );

  it("polls again after cancelling a completed migration", async () => {
    h.getMig
      .mockResolvedValueOnce(status("completed"))
      .mockResolvedValue(status("failed", "9"));
    h.cancel.mockResolvedValue(status("rolling_back", "8"));

    render(<StorageMigrationCard tenantId="t-1" storageLayout="dedicated" />);
    await userEvent.click(
      await screen.findByRole("button", { name: /cancel migration/i }),
    );
    const dialog = await screen.findByRole("alertdialog");
    await userEvent.click(
      within(dialog).getByRole("button", { name: /cancel migration/i }),
    );
    // The completed status stopped the loop; the cancel starts it again,
    // and the next read is the rollback's end.
    expect(await screen.findByText("failed")).toBeInTheDocument();
    expect(h.getMig.mock.calls.length).toBeGreaterThanOrEqual(2);
  });
});
