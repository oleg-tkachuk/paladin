import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// The budget page reads/writes tenantBudgetClient and pulls tenantId from the
// tenant context; mock both plus notifications.
const h = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn(), notify: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  tenantBudgetClient: { get: h.get, set: h.set },
}));
vi.mock("../tenant-context", () => ({
  useTenant: () => ({ tenantId: "t-1", displayName: "Acme" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));

import BudgetPage from "./page";

const budget = {
  $typeName: "paladin.admin.v1.TenantBudget",
  maxBudgetAmount: 100,
  spentAmount: 0,
  unitCode: "USD",
  periodStart: undefined,
  periodEnd: undefined,
};

describe("TenantBudgetPage period close", () => {
  beforeEach(() => {
    h.get.mockReset();
    h.set.mockReset();
    h.notify.mockReset();
    h.set.mockResolvedValue({});
  });

  it("sends period_end as a Timestamp when a close date is set", async () => {
    h.get.mockResolvedValue({ budget });
    render(<BudgetPage />);

    // Wait for the query to resolve — "Apply changes" only shows once an
    // existing budget is loaded (vs "Create budget" on NotFound).
    const submit = await screen.findByRole("button", {
      name: /apply changes/i,
    });

    await userEvent.type(screen.getByLabelText(/period close/i), "2026-08-01");
    await userEvent.click(submit);

    await waitFor(() => expect(h.set).toHaveBeenCalledTimes(1));
    const arg = h.set.mock.calls[0][0];
    expect(arg.periodEnd).toBeDefined();
    expect(Number(arg.periodEnd.seconds)).toBe(
      Math.floor(Date.parse("2026-08-01T00:00:00Z") / 1000),
    );
  });

  it("omits period_end when the close date is blank", async () => {
    h.get.mockResolvedValue({ budget });
    render(<BudgetPage />);
    const submit = await screen.findByRole("button", {
      name: /apply changes/i,
    });

    await userEvent.click(submit);

    await waitFor(() => expect(h.set).toHaveBeenCalledTimes(1));
    expect(h.set.mock.calls[0][0].periodEnd).toBeUndefined();
  });
});
