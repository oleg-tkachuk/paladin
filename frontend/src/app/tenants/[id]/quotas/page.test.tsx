import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// "Reset daily counters" zeroes today's usage — the tenant gets its whole
// daily allowance back and the usage so far is not kept anywhere. It was one
// click on an outline button.
const h = vi.hoisted(() => ({
  getQuota: vi.fn(),
  setQuota: vi.fn(),
  resetUsage: vi.fn(),
  blocked: null as string | null,
}));

vi.mock("@/lib/connect/client", () => ({
  quotaClient: {
    getQuota: h.getQuota,
    setQuota: h.setQuota,
    resetUsage: h.resetUsage,
  },
}));
vi.mock("../tenant-context", () => ({
  useTenantChangesBlocked: () => h.blocked,
  useTenant: () => ({ tenantId: "t-1", slug: "acme", displayName: "Acme" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

import TenantQuotasPage from "./page";

beforeEach(() => {
  h.blocked = null;
  h.getQuota.mockReset();
  h.getQuota.mockResolvedValue({
    name: "tenants/t-1/quota",
    resourceVersion: "2",
    usage: {},
  });
  h.resetUsage.mockReset();
  h.resetUsage.mockResolvedValue({});
  h.setQuota.mockReset();
  h.setQuota.mockResolvedValue({});
});

async function pressReset() {
  render(<TenantQuotasPage />);
  const button = await screen.findByRole("button", {
    name: "Reset daily counters",
  });
  await waitFor(() => expect(button).toBeEnabled());
  await userEvent.click(button);
}

describe("TenantQuotasPage reset", () => {
  it("asks before zeroing today's counters", async () => {
    await pressReset();
    expect(h.resetUsage).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toHaveTextContent(
      /full daily allowance again/,
    );
  });

  it("resets once confirmed", async () => {
    await pressReset();
    await userEvent.click(
      screen.getByRole("button", { name: "Reset counters" }),
    );
    await waitFor(() =>
      expect(h.resetUsage).toHaveBeenCalledWith({ name: "tenants/t-1/quota" }),
    );
  });
});

// A failed read left an empty card and Reset disabled, as if unconfigured.
describe("TenantQuotasPage failed read", () => {
  it("says the quota could not be loaded", async () => {
    h.getQuota.mockRejectedValue(new Error("unavailable"));
    render(<TenantQuotasPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Quota could not be loaded/,
    );
  });
});

// A trashed tenant takes no changes; the control says so instead of failing.
describe("TenantQuotasPage on a trashed tenant", () => {
  it("holds the reset and says why", async () => {
    h.blocked = "in the trash";
    render(<TenantQuotasPage />);
    const button = await screen.findByRole("button", {
      name: "Reset daily counters",
    });
    await waitFor(() => expect(h.getQuota).toHaveBeenCalled());
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute("title", "in the trash");
  });
});

// A cap of 0 allows nothing; "no cap" is its own state, sent as a cap the
// mask names and the quota leaves absent. 0 used to mean "no cap".
describe("TenantQuotasPage caps", () => {
  const toggleOf = (id: string) =>
    document
      .getElementById(id)!
      .parentElement!.querySelector("button[aria-pressed]") as HTMLElement;

  it("shows a cap of 0 as a limit and an absent cap as unlimited", async () => {
    h.getQuota.mockResolvedValue({
      name: "tenants/t-1/quota",
      resourceVersion: "2",
      maxTotalBytes: BigInt(0),
      usage: {},
    });
    render(<TenantQuotasPage />);
    await waitFor(() =>
      expect(document.getElementById("max-total-bytes")).toHaveValue(0),
    );
    expect(toggleOf("max-total-bytes")).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    expect(toggleOf("max-object-count")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByText("cap reached")).toBeInTheDocument();
  });

  it("sends 0 as a cap and Unlimited as an absent one, every cap masked", async () => {
    render(<TenantQuotasPage />);
    const submit = await screen.findByRole("button", {
      name: /apply changes/i,
    });
    await userEvent.click(toggleOf("max-object-count"));
    await userEvent.type(document.getElementById("max-object-count")!, "0");
    await userEvent.click(submit);

    await waitFor(() => expect(h.setQuota).toHaveBeenCalledTimes(1));
    const req = h.setQuota.mock.calls[0][0];
    expect(req.quota.maxObjectCount).toBe(BigInt(0));
    expect(req.quota.maxTotalBytes).toBeUndefined();
    expect(req.updateMask.paths).toEqual([
      "max_total_bytes",
      "max_object_count",
      "max_bytes_per_day",
      "max_objects_per_day",
    ]);
  });

  it("holds the submit while a cap is on but not a whole number", async () => {
    render(<TenantQuotasPage />);
    const submit = await screen.findByRole("button", {
      name: /apply changes/i,
    });
    await userEvent.click(toggleOf("max-total-bytes"));
    expect(submit).toBeDisabled();
    await userEvent.type(document.getElementById("max-total-bytes")!, "12");
    expect(submit).toBeEnabled();
  });
});
