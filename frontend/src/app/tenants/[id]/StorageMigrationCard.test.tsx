import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// StorageMigrationCard drives the tenant client through useTenants; mock the
// client. getTenantStorageMigration → null models "no migration yet".
const h = vi.hoisted(() => ({
  getMig: vi.fn(),
  migrate: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  tenantClient: {
    getTenantStorageMigration: h.getMig,
    migrateTenantStorageLayout: h.migrate,
  },
}));

import { StorageMigrationCard } from "./StorageMigrationCard";

describe("StorageMigrationCard", () => {
  beforeEach(() => {
    h.getMig.mockReset();
    h.migrate.mockReset();
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

  it("renders no migrate trigger for a dedicated tenant with no migration", async () => {
    h.getMig.mockResolvedValue(null);
    render(<StorageMigrationCard tenantId="t-1" storageLayout="dedicated" />);
    await waitFor(() => expect(h.getMig).toHaveBeenCalled());
    expect(
      screen.queryByRole("button", { name: /migrate to dedicated/i }),
    ).toBeNull();
  });
});
