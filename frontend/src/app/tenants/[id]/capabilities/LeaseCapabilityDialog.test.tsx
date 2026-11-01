import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({
  lease: vi.fn(),
  settleLease: vi.fn(),
  notify: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: { lease: h.lease, settleLease: h.settleLease },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));

import {
  LeaseCapabilityDialog,
  SettleLeaseDialog,
} from "./LeaseCapabilityDialog";

describe("LeaseCapabilityDialog", () => {
  beforeEach(() => {
    h.lease.mockReset().mockResolvedValue({});
    h.notify.mockReset();
  });

  it("leases what was typed, in the capability's unit", async () => {
    const onClose = vi.fn();
    render(<LeaseCapabilityDialog id="cap-1" unit="EUR" onClose={onClose} />);
    const confirm = screen.getByRole("button", { name: "Lease" });
    expect(confirm).toBeDisabled();
    await userEvent.type(screen.getByLabelText("Budget (EUR)"), "2.5");
    await userEvent.type(screen.getByLabelText("Requests"), "7");
    await userEvent.type(screen.getByLabelText("Lasts (minutes)"), "2");
    await userEvent.click(confirm);
    await waitFor(() => expect(h.lease).toHaveBeenCalledTimes(1));
    const req = h.lease.mock.calls[0][0];
    expect(req).toMatchObject({
      id: "cap-1",
      amount: { currencyCode: "EUR", units: 2n, nanos: 500_000_000 },
      requests: 7n,
      ttl: { seconds: 120n },
    });
    expect(onClose).toHaveBeenCalled();
    expect(h.notify).toHaveBeenCalledWith(
      expect.objectContaining({ title: "Budget leased" }),
    );
  });

  it("says what is wrong with the form and holds the lease", async () => {
    render(<LeaseCapabilityDialog id="cap-1" unit="EUR" onClose={vi.fn()} />);
    await userEvent.type(screen.getByLabelText("Requests"), "1.5");
    expect(screen.getByRole("alert")).toHaveTextContent("whole number");
    expect(screen.getByRole("button", { name: "Lease" })).toBeDisabled();
  });

  it("says why nothing was leased", async () => {
    h.lease.mockRejectedValue(new Error("tenant budget exceeded"));
    render(<LeaseCapabilityDialog id="cap-1" unit="EUR" onClose={vi.fn()} />);
    await userEvent.type(screen.getByLabelText("Requests"), "1");
    await userEvent.click(screen.getByRole("button", { name: "Lease" }));
    await waitFor(() =>
      expect(h.notify).toHaveBeenCalledWith(
        expect.objectContaining({ type: "error", title: "Lease failed" }),
      ),
    );
  });
});

describe("SettleLeaseDialog", () => {
  beforeEach(() => {
    h.settleLease.mockReset().mockResolvedValue({});
    h.notify.mockReset();
  });

  it("settles the capability's lease once confirmed", async () => {
    const onClose = vi.fn();
    render(<SettleLeaseDialog id="cap-1" onClose={onClose} />);
    await userEvent.click(screen.getByRole("button", { name: "Settle lease" }));
    await waitFor(() =>
      expect(h.settleLease).toHaveBeenCalledWith({ id: "cap-1" }),
    );
    expect(onClose).toHaveBeenCalled();
  });

  it("says why the lease was not settled", async () => {
    h.settleLease.mockRejectedValue(new Error("no lease"));
    render(<SettleLeaseDialog id="cap-1" onClose={vi.fn()} />);
    await userEvent.click(screen.getByRole("button", { name: "Settle lease" }));
    await waitFor(() =>
      expect(h.notify).toHaveBeenCalledWith(
        expect.objectContaining({ type: "error", title: "Settle failed" }),
      ),
    );
  });
});
