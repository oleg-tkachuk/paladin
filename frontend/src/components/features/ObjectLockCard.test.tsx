import React from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi, beforeEach } from "vitest";

import { ObjectLockCard } from "./ObjectLockCard";

// The card's job is to make an irreversible choice legible before it is made,
// so these tests are mostly about what the user is shown and how many
// deliberate acts stand between them and a COMPLIANCE lock.

const h = vi.hoisted(() => ({
  lock: null as {
    mode: string;
    retainUntil?: { seconds: bigint };
    legalHold: boolean;
  } | null,
  isLoading: false,
  error: null as string | null,
  setRetention: vi.fn(),
  setLegalHold: vi.fn(),
  refresh: vi.fn(),
}));

vi.mock("@/hooks/useObjectLock", () => ({
  useObjectLock: () => ({
    lock: h.lock,
    isLoading: h.isLoading,
    error: h.error,
    setRetention: h.setRetention,
    setLegalHold: h.setLegalHold,
    refresh: h.refresh,
  }),
}));

const NAME = "tenants/t1/collections/docs/objects/o1";

function tsIn(days: number) {
  return { seconds: BigInt(Math.floor(Date.now() / 1000) + days * 86400) };
}

beforeEach(() => {
  h.lock = null;
  h.isLoading = false;
  h.error = null;
  h.setRetention.mockReset().mockResolvedValue(undefined);
  h.setLegalHold.mockReset().mockResolvedValue(undefined);
});

describe("ObjectLockCard", () => {
  it("says plainly that an unlocked object can be deleted", () => {
    render(<ObjectLockCard objectName={NAME} />);
    expect(screen.getByText(/no retention window/i)).toBeInTheDocument();
  });

  it("names the mode and the date for a retained object", () => {
    h.lock = { mode: "COMPLIANCE", retainUntil: tsIn(30), legalHold: false };
    render(<ObjectLockCard objectName={NAME} />);
    // "COMPLIANCE" also names an option in the mode picker, so match the
    // status line rather than the bare word.
    expect(screen.getByText(/retained under/i).textContent).toMatch(
      /COMPLIANCE/,
    );
    expect(
      screen.getByText(/cannot be shortened by anyone/i),
    ).toBeInTheDocument();
  });

  it("applies a GOVERNANCE window in one click", async () => {
    const user = userEvent.setup();
    render(<ObjectLockCard objectName={NAME} />);

    await user.type(screen.getByLabelText(/retain until/i), "2030-01-01T00:00");
    await user.click(screen.getByRole("button", { name: /apply retention/i }));

    await waitFor(() => expect(h.setRetention).toHaveBeenCalledTimes(1));
    expect(h.setRetention.mock.calls[0][0]).toMatchObject({
      mode: "GOVERNANCE",
      bypassGovernance: false,
    });
  });

  // COMPLIANCE is the one choice nothing can undo — not the tenant admin, not
  // the platform admin, not the person clicking. One click is the wrong number
  // of clicks for that.
  it("makes COMPLIANCE take a second, explicit confirmation", async () => {
    const user = userEvent.setup();
    render(<ObjectLockCard objectName={NAME} />);

    await user.click(screen.getByRole("combobox"));
    await user.click(screen.getByRole("option", { name: "COMPLIANCE" }));
    await user.type(screen.getByLabelText(/retain until/i), "2030-01-01T00:00");

    // The consequence has to be on screen before the commit is available.
    expect(
      screen.getByText(/cannot be shortened, downgraded or removed/i),
    ).toBeInTheDocument();

    await user.click(
      screen.getByRole("button", { name: /apply compliance retention/i }),
    );
    expect(h.setRetention).not.toHaveBeenCalled();

    await user.click(
      screen.getByRole("button", { name: /pin permanently until that date/i }),
    );
    await waitFor(() => expect(h.setRetention).toHaveBeenCalledTimes(1));
    expect(h.setRetention.mock.calls[0][0]).toMatchObject({
      mode: "COMPLIANCE",
    });
  });

  it("offers the governance bypass only against an active GOVERNANCE window", () => {
    h.lock = { mode: "GOVERNANCE", retainUntil: tsIn(10), legalHold: false };
    const { unmount } = render(<ObjectLockCard objectName={NAME} />);
    expect(screen.getByText(/use governance bypass/i)).toBeInTheDocument();
    unmount();

    // Not offered against COMPLIANCE, where it does nothing.
    h.lock = { mode: "COMPLIANCE", retainUntil: tsIn(10), legalHold: false };
    render(<ObjectLockCard objectName={NAME} />);
    expect(
      screen.queryByText(/use governance bypass/i),
    ).not.toBeInTheDocument();
  });

  it("does not offer the bypass on an object with no window", () => {
    render(<ObjectLockCard objectName={NAME} />);
    expect(
      screen.queryByText(/use governance bypass/i),
    ).not.toBeInTheDocument();
  });

  it("toggles the legal hold and explains what it does", async () => {
    const user = userEvent.setup();
    render(<ObjectLockCard objectName={NAME} />);

    expect(
      screen.getByText(/blocks deletion indefinitely/i),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("switch", { name: /legal hold/i }));
    await waitFor(() => expect(h.setLegalHold).toHaveBeenCalledWith(true));
  });

  it("states that the bypass does not lift a hold", () => {
    h.lock = { mode: "", legalHold: true };
    render(<ObjectLockCard objectName={NAME} />);
    expect(
      screen.getByText(/governance bypass does not lift it/i),
    ).toBeInTheDocument();
  });

  it("shows a read error instead of an empty card", () => {
    h.error = "permission denied";
    render(<ObjectLockCard objectName={NAME} />);
    expect(screen.getByText("permission denied")).toBeInTheDocument();
  });

  it("will not submit an empty date", async () => {
    const user = userEvent.setup();
    render(<ObjectLockCard objectName={NAME} />);
    await user.click(screen.getByRole("button", { name: /apply retention/i }));
    expect(h.setRetention).not.toHaveBeenCalled();
  });
});
