import { describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { ConnectError, Code } from "@connectrpc/connect";

import { render, screen, within } from "@/test/utils";
import { moneyFromDecimal } from "@/lib/format/money";

// Each test sets the answer it needs; clearing the mock between tests makes
// Vitest report the rejected answer of the failure case as the test's error.
const h = vi.hoisted(() => ({ listReservations: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  tenantBudgetClient: { listReservations: h.listReservations },
}));

import { LAPSED_LABEL, TenantReservations } from "./TenantReservations";

const NOW = new Date("2026-10-31T12:00:00Z");
const hold = (id: string, capabilityId: string, expires: string) => ({
  id,
  capabilityId,
  amount: moneyFromDecimal("1.5", "EUR"),
  op: "get",
  actor: "agent",
  expiresAt: timestampFromDate(new Date(expires)),
});

describe("TenantReservations", () => {
  it("lists each hold with its capability linked, and marks one past its expiry", async () => {
    h.listReservations.mockResolvedValue({
      reservations: [
        hold("h1", "cap-lapsed", "2026-10-31T11:00:00Z"),
        hold("h2", "cap-open", "2026-10-31T13:00:00Z"),
      ],
      nextPageToken: "",
    });
    render(<TenantReservations tenant="acme" tenantId="t-1" now={NOW} />);

    const lapsed = (
      await screen.findByRole("link", { name: "cap-lapsed" })
    ).closest("tr")!;
    expect(screen.getByRole("link", { name: "cap-lapsed" })).toHaveAttribute(
      "href",
      "/tenants/acme/capabilities?id=cap-lapsed",
    );
    expect(within(lapsed).getByText(LAPSED_LABEL)).toBeInTheDocument();
    expect(within(lapsed).getByText(/1\.5/)).toBeInTheDocument();
    const open = screen.getByRole("link", { name: "cap-open" }).closest("tr")!;
    expect(within(open).queryByText(LAPSED_LABEL)).toBeNull();
    expect(h.listReservations).toHaveBeenCalledWith(
      expect.objectContaining({ tenantId: "t-1", pageToken: "" }),
      expect.anything(),
    );
  });

  it("reads the next page when asked", async () => {
    h.listReservations
      .mockResolvedValueOnce({
        reservations: [hold("h1", "cap-1", "2026-10-31T13:00:00Z")],
        nextPageToken: "p2",
      })
      .mockResolvedValueOnce({
        reservations: [hold("h2", "cap-2", "2026-10-31T14:00:00Z")],
        nextPageToken: "",
      });
    render(<TenantReservations tenant="acme" tenantId="t-1" now={NOW} />);

    await userEvent.click(
      await screen.findByRole("button", { name: "Show more" }),
    );
    expect(
      await screen.findByRole("link", { name: "cap-2" }),
    ).toBeInTheDocument();
    expect(h.listReservations).toHaveBeenLastCalledWith(
      expect.objectContaining({ pageToken: "p2" }),
      expect.anything(),
    );
    expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
  });

  it("says there are none", async () => {
    h.listReservations.mockResolvedValue({
      reservations: [],
      nextPageToken: "",
    });
    render(<TenantReservations tenant="acme" tenantId="t-1" now={NOW} />);
    expect(await screen.findByText(/no holds/i)).toBeInTheDocument();
  });

  it("says why the holds did not load, rather than that there are none", async () => {
    h.listReservations.mockRejectedValue(
      new ConnectError("denied", Code.PermissionDenied),
    );
    render(<TenantReservations tenant="acme" tenantId="t-1" now={NOW} />);
    expect(await screen.findByText(/denied/)).toBeInTheDocument();
    expect(screen.queryByText(/no holds/i)).toBeNull();
  });
});
