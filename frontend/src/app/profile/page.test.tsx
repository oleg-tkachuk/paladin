import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({ getMine: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  userSettingsClient: { getMine: h.getMine, updateMine: vi.fn() },
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({
    user: { subject: "admin", tenantId: "t-1", roles: ["platform.admin"] },
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/components/features/ChangePasswordCard", () => ({
  ChangePasswordCard: () => null,
}));

import ProfilePage from "./page";
import { SETTINGS_APPLIED_NOTE } from "./_constants";

const DEFAULTS = {
  timezone: "",
  locale: "",
  theme: "",
  resourceVersion: "0",
};

describe("ProfilePage sync footer", () => {
  beforeEach(() => {
    h.getMine.mockReset();
  });

  it("does not report a sync time for settings that were never saved", async () => {
    h.getMine.mockResolvedValue(DEFAULTS);
    render(<ProfilePage />);

    expect(
      await screen.findByText("Defaults — not saved yet."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Last synced/)).toBeNull();
  });

  it("reports when saved settings were last synced", async () => {
    h.getMine.mockResolvedValue({
      ...DEFAULTS,
      resourceVersion: "3",
      updatedAt: { seconds: BigInt(Date.parse("2026-09-30T10:00:00Z") / 1000) },
    });
    render(<ProfilePage />);

    expect(
      await screen.findByText(/Last synced 2026-09-30 10:00:00Z/),
    ).toBeInTheDocument();
  });
});

describe("ProfilePage preferences", () => {
  beforeEach(() => {
    h.getMine.mockReset();
  });

  // The console applies the theme only; the card says which settings take
  // effect rather than presenting all three as if they did.
  it("says which settings the console applies", async () => {
    h.getMine.mockResolvedValue(DEFAULTS);
    render(<ProfilePage />);
    expect(await screen.findByText(SETTINGS_APPLIED_NOTE)).toBeInTheDocument();
  });
});

// A failed read showed the defaults and "not saved yet", with Save ready to
// write those defaults over the real settings.
describe("ProfilePage failed read", () => {
  beforeEach(() => {
    h.getMine.mockReset();
  });

  it("says the preferences could not be loaded, and offers no Save", async () => {
    h.getMine.mockRejectedValue(new Error("unavailable"));
    render(<ProfilePage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Preferences could not be loaded/,
    );
    expect(screen.queryByText("Defaults — not saved yet.")).toBeNull();
    expect(screen.queryByRole("button", { name: /^Save/ })).toBeNull();
  });
});
