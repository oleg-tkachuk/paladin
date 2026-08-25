import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";
import { Code, ConnectError } from "@connectrpc/connect";

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
  resourceVersion: "7",
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

describe("TenantBudgetPage OCC", () => {
  beforeEach(() => {
    h.get.mockReset();
    h.set.mockReset();
    h.notify.mockReset();
    h.set.mockResolvedValue({});
  });

  it("sends the version it read, so a concurrent edit is refused server-side", async () => {
    h.get.mockResolvedValue({ budget });
    render(<BudgetPage />);

    await userEvent.click(
      await screen.findByRole("button", { name: /apply changes/i }),
    );

    await waitFor(() => expect(h.set).toHaveBeenCalledTimes(1));
    expect(h.set.mock.calls[0][0].resourceVersion).toBe("7");
  });

  it('sends "0" when no budget exists yet — the create case', async () => {
    h.get.mockRejectedValue(new ConnectError("nope", Code.NotFound));
    render(<BudgetPage />);

    const submit = await screen.findByRole("button", {
      name: /create budget/i,
    });
    await userEvent.type(screen.getByLabelText(/max budget/i), "50");
    await userEvent.click(submit);

    await waitFor(() => expect(h.set).toHaveBeenCalledTimes(1));
    expect(h.set.mock.calls[0][0].resourceVersion).toBe("0");
  });

  it("refetches and explains the conflict when the server aborts", async () => {
    h.get.mockResolvedValue({ budget });
    h.set.mockRejectedValue(new ConnectError("stale", Code.Aborted));
    render(<BudgetPage />);

    await userEvent.click(
      await screen.findByRole("button", { name: /apply changes/i }),
    );

    await waitFor(() =>
      expect(h.notify).toHaveBeenCalledWith(
        expect.objectContaining({ title: "Budget changed elsewhere" }),
      ),
    );
    // The stale snapshot must not stay on screen: the page re-reads so the
    // next Apply carries the version that actually won.
    expect(h.get.mock.calls.length).toBeGreaterThan(1);
  });
});
