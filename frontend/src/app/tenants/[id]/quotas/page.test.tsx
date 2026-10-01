import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// "Reset daily counters" zeroes today's usage — the tenant gets its whole
// daily allowance back and the usage so far is not kept anywhere. It was one
// click on an outline button.
const h = vi.hoisted(() => ({
  getQuota: vi.fn(),
  resetUsage: vi.fn(),
}));

vi.mock("@/lib/connect/client", () => ({
  quotaClient: { getQuota: h.getQuota, resetUsage: h.resetUsage },
}));
vi.mock("../tenant-context", () => ({
  useTenant: () => ({ tenantId: "t-1", slug: "acme", displayName: "Acme" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

import TenantQuotasPage from "./page";

beforeEach(() => {
  h.getQuota.mockReset();
  h.getQuota.mockResolvedValue({
    name: "tenants/t-1/quota",
    resourceVersion: "2",
    usage: {},
  });
  h.resetUsage.mockReset();
  h.resetUsage.mockResolvedValue({});
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
