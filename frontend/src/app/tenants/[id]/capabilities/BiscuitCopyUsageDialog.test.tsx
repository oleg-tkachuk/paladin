import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@/test/utils";

const h = vi.hoisted(() => ({
  getBiscuitUsage: vi.fn(),
  showNotification: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: { getBiscuitUsage: h.getBiscuitUsage },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { BiscuitCopyUsageDialog } from "./BiscuitCopyUsageDialog";
import { shortRevocationId } from "./_biscuit";
import { moneyFromDecimal } from "@/lib/format/money";

const xxx = (amount: string) => moneyFromDecimal(amount, "XXX");

const BISCUIT = "En0KEwoEMTIzNBgDIgkKBwgKEgMYgAgSJAgAEiB";
const JWT = "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJ4In0.c2ln";
const INNER = new Uint8Array([0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04]);
const OUTER = new Uint8Array([0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10]);

function open() {
  render(<BiscuitCopyUsageDialog open onClose={vi.fn()} />);
}
const tokenBox = () => screen.getByLabelText("Biscuit");
const check = () => screen.getByRole("button", { name: "Check usage" });

describe("BiscuitCopyUsageDialog", () => {
  beforeEach(() => {
    h.getBiscuitUsage.mockReset();
    h.showNotification.mockReset();
  });

  it("holds the lookup until a Biscuit is pasted, and refuses a JWT", () => {
    open();
    expect(check()).toBeDisabled();
    fireEvent.change(tokenBox(), { target: { value: JWT } });
    expect(check()).toBeDisabled();
    expect(screen.getByText(/A JWT has no copies/)).toBeInTheDocument();
  });

  it("shows each limit with what has been used against it", async () => {
    h.getBiscuitUsage.mockResolvedValue({
      capabilityId: "cap-1",
      copies: [
        {
          revocationId: INNER,
          maxRequests: 10n,
          maxBudget: xxx("0"),
          requestCount: 3n,
          spent: xxx("0"),
          reserved: xxx("0"),
        },
        {
          revocationId: OUTER,
          maxRequests: 0n,
          maxBudget: xxx("2"),
          requestCount: 7n,
          spent: xxx("1.25"),
          reserved: xxx("0.5"),
        },
      ],
    });
    open();
    fireEvent.change(tokenBox(), { target: { value: ` ${BISCUIT}\n` } });
    fireEvent.click(check());
    await waitFor(() =>
      expect(h.getBiscuitUsage).toHaveBeenCalledWith({ token: BISCUIT }),
    );
    const result = await screen.findByTestId("copy-usage-result");
    expect(within(result).getByText("cap-1")).toBeInTheDocument();
    const rows = within(result).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    const cells = (row: HTMLElement) =>
      within(row)
        .getAllByRole("cell")
        .map((c) => c.textContent?.trim());
    const [inner, outer] = rows.map(cells);
    expect(inner[0]).toBe(`${shortRevocationId(INNER)}(innermost)`);
    expect(inner[1]).toBe("3 / 10");
    expect(outer[0]).toBe(shortRevocationId(OUTER));
    expect(outer[1]).toBe("7"); // no request limit at this block
    // XXX reads as a number of units, never with the generic "¤" sign.
    expect(outer[2]).toBe("1.2500 units / 2.0000 units");
    expect(outer[3]).toBe("0.5000 units");
  });

  it("says when no limits were narrowed onto the copy", async () => {
    h.getBiscuitUsage.mockResolvedValue({
      capabilityId: "cap-1",
      copies: [],
    });
    open();
    fireEvent.change(tokenBox(), { target: { value: BISCUIT } });
    fireEvent.click(check());
    expect(
      await screen.findByText(/No limits were narrowed onto this copy/),
    ).toBeInTheDocument();
  });

  it("reports a refusal from the server and shows no stale result", async () => {
    h.getBiscuitUsage.mockResolvedValueOnce({
      capabilityId: "cap-1",
      copies: [],
    });
    open();
    fireEvent.change(tokenBox(), { target: { value: BISCUIT } });
    fireEvent.click(check());
    await screen.findByTestId("copy-usage-result");

    // The same copy, checked again after it was revoked.
    h.getBiscuitUsage.mockRejectedValue(new Error("token: invalid signature"));
    fireEvent.click(check());
    await waitFor(() =>
      expect(h.showNotification).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "error",
          title: "Usage lookup failed",
        }),
      ),
    );
    expect(screen.queryByTestId("copy-usage-result")).not.toBeInTheDocument();
  });
});

describe("shortRevocationId", () => {
  it("shows the leading bytes as hex", () => {
    expect(shortRevocationId(INNER)).toBe("deadbeef0102");
  });
});
