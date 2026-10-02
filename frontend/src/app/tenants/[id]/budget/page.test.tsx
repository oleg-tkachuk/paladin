import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@/test/utils";
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
  maxBudgetMicros: 100_000_000n,
  spentMicros: 0n,
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

  // A refetch during an edit kept the operator's values but moved the version
  // forward, so their stale edit passed the OCC check and overwrote a change
  // made in the meantime. The cluster e2e caught it when a refetch landed
  // between the other write and Apply.
  it("sends the version the form was filled from, not the latest read", async () => {
    h.get.mockResolvedValue({ budget });
    render(<BudgetPage />);
    const field = await screen.findByLabelText(/max budget/i);
    await waitFor(() => expect(field).toHaveValue(100));
    fireEvent.change(field, { target: { value: "999" } });

    // Another operator's write lands; a refresh reads it mid-edit.
    h.get.mockResolvedValue({
      budget: {
        ...budget,
        maxBudgetAmount: 300,
        maxBudgetMicros: 300_000_000n,
        resourceVersion: "8",
      },
    });
    await userEvent.click(screen.getByRole("button", { name: /refresh/i }));
    await waitFor(() => expect(h.get).toHaveBeenCalledTimes(2));

    await userEvent.click(
      screen.getByRole("button", { name: /apply changes/i }),
    );
    await waitFor(() => expect(h.set).toHaveBeenCalledTimes(1));
    expect(h.set.mock.calls[0][0].resourceVersion).toBe("7");
    expect(h.set.mock.calls[0][0].maxBudgetMicros).toBe(999_000_000n);
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

  it("holds submit until the first read resolves, so it cannot send a version it never read", async () => {
    // Never resolves: the page has no snapshot, so it does not know whether
    // this is a create or an update.
    h.get.mockReturnValue(new Promise(() => {}));
    render(<BudgetPage />);

    // Label is whatever the not-yet-known state renders; what matters is that
    // it cannot be pressed.
    const submit = await screen.findByRole("button", {
      name: /create budget|apply changes/i,
    });
    expect(submit).toBeDisabled();
  });

  it("keeps submit usable while a background refetch is in flight", async () => {
    h.get.mockResolvedValue({ budget });
    render(<BudgetPage />);

    const submit = await screen.findByRole("button", {
      name: /apply changes/i,
    });

    // Refresh kicks off a refetch. The button must stay clickable: disabling it
    // on every in-flight read silently drops clicks an operator has already
    // committed to.
    await userEvent.click(screen.getByRole("button", { name: /refresh/i }));
    expect(submit).toBeEnabled();
  });

  it("stays silent when a read is cancelled rather than toasting Load failed", async () => {
    // TanStack aborts in-flight reads on unmount and on supersede. Toasting
    // that put a "Load failed — signal is aborted without reason" card in the
    // bottom-right corner, on top of the submit button, where it swallowed the
    // operator's click.
    h.get.mockRejectedValue(new ConnectError("aborted", Code.Canceled));
    render(<BudgetPage />);

    await waitFor(() => expect(h.get).toHaveBeenCalled());
    expect(h.notify).not.toHaveBeenCalled();
  });

  it("does not overwrite an in-progress edit when a refetch lands", async () => {
    h.get.mockResolvedValue({ budget });
    render(<BudgetPage />);

    const submit = await screen.findByRole("button", {
      name: /apply changes/i,
    });

    // jsdom does not support selection on <input type="number">, so typing
    // appends to the seeded 100 rather than replacing it. The digits do not
    // matter — what matters is that the refetch leaves them alone.
    const field = screen.getByLabelText(/max budget/i);
    await userEvent.type(field, "500");
    const typed = (field as HTMLInputElement).value;

    // Refresh re-reads the stored row while the operator is mid-edit. Seeding
    // the form from that snapshot reverted the typed cap to the stored one,
    // and Apply then submitted the old number as if nothing had been typed.
    await userEvent.click(screen.getByRole("button", { name: /refresh/i }));
    await waitFor(() => expect(h.get.mock.calls.length).toBeGreaterThan(1));
    expect(field).toHaveValue(Number(typed));

    await userEvent.click(submit);
    await waitFor(() => expect(h.set).toHaveBeenCalledTimes(1));
    expect(h.set.mock.calls[0][0].maxBudgetMicros).toBe(
      BigInt(typed) * 1_000_000n,
    );
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

describe("TenantBudgetPage unavailable", () => {
  beforeEach(() => {
    h.get.mockReset();
    h.set.mockReset();
    h.notify.mockReset();
  });

  // The toast goes away; the snapshot card must keep saying why it is empty,
  // and Apply must not look like it would work.
  it("says the budget is unavailable and holds Apply", async () => {
    h.get.mockRejectedValue(
      new ConnectError(
        "capability subsystem disabled; tenant budget unavailable",
        Code.Unavailable,
      ),
    );
    render(<BudgetPage />);

    expect(await screen.findByText("Budget unavailable.")).toBeInTheDocument();
    expect(
      screen.getByText(
        "capability subsystem disabled; tenant budget unavailable",
      ),
    ).toBeInTheDocument();
    for (const button of screen.getAllByRole("button", {
      name: /apply changes|create budget/i,
    })) {
      expect(button).toBeDisabled();
    }
  });
});

// A cap typed before the budget had loaded marked the form edited, which kept
// the loaded budget — and its version — from seeding the form; Apply then sent
// version "0" and the server refused it as a conflict.
describe("TenantBudgetPage before the budget loads", () => {
  beforeEach(() => {
    h.get.mockReset();
    h.set.mockReset();
    h.notify.mockReset();
    h.set.mockResolvedValue({});
  });

  it("takes no input until the budget arrives, then sends its version", async () => {
    let answer!: (v: unknown) => void;
    h.get.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    render(<BudgetPage />);

    const cap = screen.getByLabelText(/max budget/i);
    expect(cap).toBeDisabled();

    answer({ budget });
    await waitFor(() => expect(cap).toBeEnabled());
    expect(cap).toHaveValue(budget.maxBudgetAmount);

    await userEvent.clear(cap);
    await userEvent.type(cap, "500");
    await userEvent.click(
      screen.getByRole("button", { name: /apply changes/i }),
    );
    await waitFor(() => expect(h.set).toHaveBeenCalledTimes(1));
    expect(h.set.mock.calls[0][0].resourceVersion).toBe(budget.resourceVersion);
  });
});
