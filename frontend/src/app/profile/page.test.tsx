import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({
  getMine: vi.fn(),
  updateMine: vi.fn(),
  user: {
    userId: "0199-user-id",
    subject: "admin",
    displayName: "Ada Admin",
    tenantId: "t-1",
    tenantSlug: "acme",
    roles: ["platform.admin"] as string[],
  },
  // Set to make two different zone names one zone, as an alias is.
  sameZone: undefined as boolean | undefined,
}));
vi.mock("@/lib/format/preferences", async (importOriginal) => {
  const real =
    await importOriginal<typeof import("@/lib/format/preferences")>();
  return {
    ...real,
    isSameTimeZone: (a: string, b: string) =>
      h.sameZone ?? real.isSameTimeZone(a, b),
  };
});
vi.mock("@/lib/connect/client", () => ({
  userSettingsClient: { getMine: h.getMine, updateMine: h.updateMine },
}));
vi.mock("@/context/AuthContext", () => ({ useAuth: () => ({ user: h.user }) }));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenant: { displayName: "Acme Corp" } }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/components/features/ChangePasswordCard", () => ({
  ChangePasswordCard: () => null,
}));

import { setDisplayTimeZone } from "@/lib/format/locale";
import { USER_SETTINGS_QUERY_KEY } from "@/lib/theme";
import ProfilePage from "./page";
import { LOCALE_NOT_APPLIED_NOTE } from "./_constants";

const DEFAULTS = { timezone: "", locale: "", theme: "", resourceVersion: "0" };
const SAVED = {
  timezone: "Europe/London",
  locale: "en-GB",
  theme: "dark",
  resourceVersion: "2",
  updatedAt: { seconds: BigInt(Date.parse("2026-09-30T10:00:00Z") / 1000) },
};

beforeEach(() => {
  h.sameZone = undefined;
  h.getMine.mockReset().mockResolvedValue(SAVED);
  h.updateMine.mockReset().mockResolvedValue({});
  h.user.roles = ["platform.admin"];
});

const save = () => screen.getByRole("button", { name: "Save changes" });

describe("ProfilePage account", () => {
  it("names the user, their tenant and what each role is for", async () => {
    h.user.roles = ["platform.admin", "platform.capability-issuer"];
    render(<ProfilePage />);
    expect(screen.getByText("Ada Admin")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Acme Corp" })).toHaveAttribute(
      "href",
      "/tenants/acme",
    );
    expect(screen.getByText("platform.capability-issuer")).toHaveAttribute(
      "title",
      expect.stringMatching(/Issues capabilities for any tenant/),
    );
    expect(screen.getByText("0199-user-id")).toBeInTheDocument();
  });

  it("names a tenant.user's tenant without a link it could not open", () => {
    h.user.roles = ["tenant.user"];
    render(<ProfilePage />);
    expect(screen.getByText("Acme Corp")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Acme Corp" })).toBeNull();
  });
});

describe("ProfilePage preferences", () => {
  it("says when the settings were saved, or that they never were", async () => {
    setDisplayTimeZone("UTC");
    const { unmount } = render(<ProfilePage />);
    expect(
      await screen.findByText("2026-09-30 10:00:00 +00:00"),
    ).toBeInTheDocument();
    unmount();
    setDisplayTimeZone(undefined);

    h.getMine.mockResolvedValue(DEFAULTS);
    render(<ProfilePage />);
    expect(
      await screen.findByText("Not saved yet — these are the defaults."),
    ).toBeInTheDocument();
  });

  it("says plainly that the console does not apply the locale", async () => {
    render(<ProfilePage />);
    expect(
      await screen.findByText(LOCALE_NOT_APPLIED_NOTE),
    ).toBeInTheDocument();
  });

  it.each([
    ["Timezone", "Europe/London"],
    ["Locale", "uk-UA"],
  ])("%s is one field, with suggestions", async (label, suggestion) => {
    render(<ProfilePage />);
    const field = await screen.findByLabelText(label);
    const options = [
      ...document.querySelectorAll(
        `datalist#${field.getAttribute("list")} option`,
      ),
    ].map((o) => o.getAttribute("value"));
    expect(options).toContain(suggestion);
  });

  it("shows the time in the chosen zone, and refuses one it does not know", async () => {
    render(<ProfilePage />);
    const tz = await screen.findByLabelText("Timezone");
    expect(screen.getByText(/^Now there: \d\d:\d\d · GMT/)).toBeInTheDocument();
    await userEvent.clear(tz);
    await userEvent.type(tz, "Mars/Olympus");
    expect(tz).toHaveAttribute("aria-invalid", "true");
    expect(
      screen.getByText("Not a time zone this browser knows."),
    ).toBeInTheDocument();
    expect(save()).toBeDisabled();
  });

  it("requires a time zone and a locale, as the server does", async () => {
    render(<ProfilePage />);
    await userEvent.clear(await screen.findByLabelText("Locale"));
    expect(screen.getByText("Required.")).toBeInTheDocument();
    expect(save()).toBeDisabled();
  });

  it("shows how a locale writes numbers and dates", async () => {
    render(<ProfilePage />);
    const loc = await screen.findByLabelText("Locale");
    await userEvent.clear(loc);
    await userEvent.type(loc, "de-DE");
    expect(screen.getByText(/^Writes 1\.234\.567,89/)).toBeInTheDocument();
  });

  it("fills a field from this browser", async () => {
    render(<ProfilePage />);
    const tz = await screen.findByLabelText("Timezone");
    const detected = Intl.DateTimeFormat().resolvedOptions().timeZone;
    await userEvent.clear(tz);
    await userEvent.click(
      screen.getByRole("button", { name: `Use this browser's (${detected})` }),
    );
    expect(tz).toHaveValue(detected);
  });

  // Europe/Kyiv saved, Europe/Kiev detected: one zone under two names.
  it("offers no browser value that is already the one set", async () => {
    const detected = Intl.DateTimeFormat().resolvedOptions().timeZone;
    h.sameZone = true;
    render(<ProfilePage />);
    await screen.findByLabelText("Timezone");
    expect(
      screen.queryByRole("button", {
        name: `Use this browser's (${detected})`,
      }),
    ).toBeNull();
  });

  it("saves a theme picked from the menu", async () => {
    render(<ProfilePage />);
    const trigger = await screen.findByRole("combobox", { name: "Theme" });
    expect(trigger).toHaveTextContent("Dark");
    await userEvent.click(trigger);
    await userEvent.click(await screen.findByRole("option", { name: "Ember" }));
    const confirmed = () =>
      screen.getAllByRole("status").some((s) => s.textContent === "Saved");
    expect(confirmed()).toBe(false);
    await userEvent.click(save());
    await waitFor(() =>
      expect(h.updateMine).toHaveBeenCalledWith(
        expect.objectContaining({
          theme: "ember",
          timezone: "Europe/London",
          locale: "en-GB",
        }),
      ),
    );
    // Confirmed beside Save, where it was pressed.
    await waitFor(() => expect(confirmed()).toBe(true));
  });

  it("says the preferences could not be loaded, and offers no Save", async () => {
    h.getMine.mockRejectedValue(new Error("unavailable"));
    render(<ProfilePage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Preferences could not be loaded/,
    );
    expect(screen.queryByRole("button", { name: /^Save/ })).toBeNull();
  });

  // The shell reads the same settings query on sign-in, so by the time this
  // page mounts the snapshot is usually cached already. The form must show it.
  it("shows settings already in the cache", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    client.setQueryData(USER_SETTINGS_QUERY_KEY, {
      ...SAVED,
      timezone: "Asia/Tokyo",
      locale: "uk-UA",
    });
    render(
      <QueryClientProvider client={client}>
        <ProfilePage />
      </QueryClientProvider>,
    );
    expect(await screen.findByLabelText("Timezone")).toHaveValue("Asia/Tokyo");
    expect(screen.getByLabelText("Locale")).toHaveValue("uk-UA");
  });
});
