import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({ setEnabled: vi.fn(async () => {}) }));
vi.mock("@/hooks/usePreference", () => ({
  usePreference: () => [false, h.setEnabled],
}));

import { NotifyOperationsField, notifyBlocker } from "./NotifyOperationsField";

function stubNotification(permission: string, answer: string) {
  vi.stubGlobal(
    "Notification",
    Object.assign(function () {}, {
      permission,
      requestPermission: vi.fn(async () => answer),
    }),
  );
}

beforeEach(() => {
  h.setEnabled.mockClear();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("NotifyOperationsField", () => {
  it("asks the browser first, and saves only when it agrees", async () => {
    stubNotification("default", "granted");
    render(<NotifyOperationsField />);
    await userEvent.click(screen.getByRole("switch"));
    await waitFor(() => expect(h.setEnabled).toHaveBeenCalledWith(true));
  });

  it("saves nothing and says why when the browser refuses", async () => {
    stubNotification("default", "denied");
    render(<NotifyOperationsField />);
    await userEvent.click(screen.getByRole("switch"));
    expect(await screen.findByRole("status")).toHaveTextContent(/blocked/);
    expect(h.setEnabled).not.toHaveBeenCalled();
  });
});

describe("notifyBlocker", () => {
  it.each([
    [false, undefined, /cannot/],
    [true, "denied", /blocked/],
  ] as const)("supported=%s permission=%s", (supported, permission, want) => {
    expect(notifyBlocker(supported, permission)).toMatch(want);
  });
  it("lets a granted or undecided browser through", () => {
    expect(notifyBlocker(true, "granted")).toBeNull();
    expect(notifyBlocker(true, "default")).toBeNull();
  });
});
