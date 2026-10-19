import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";

import { render, screen, within } from "@/test/utils";

const h = vi.hoisted(() => ({
  config: {
    config: "" as string | null,
    path: "/app/configs/config.yaml" as string | null,
    loading: false,
    error: null as Error | null,
    refresh: vi.fn(),
  },
  copy: vi.fn(),
  notify: vi.fn(),
}));

vi.mock("@/hooks/useConfig", () => ({ useConfig: () => h.config }));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({ actions }: { actions?: React.ReactNode }) => (
    <div>{actions}</div>
  ),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));
vi.mock("@/lib/utils", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/utils")>()),
  copyToClipboard: h.copy,
}));

import ConfigPage from "./page";

const YAML = `app:
  name: paladin-core
logger:
  level: info
  development: false
datastores:
  postgres:
    dsn: postgres://paladin_app@pg/paladin
    password: '***'
`;

beforeEach(() => {
  h.config.config = YAML;
  h.config.error = null;
  h.config.loading = false;
  h.copy.mockReset().mockResolvedValue(true);
  h.notify.mockReset();
});

const section = (key: string) => screen.getByRole("region", { name: key });

describe("ConfigPage", () => {
  it("lists each section with its settings, and a way to each", () => {
    render(<ConfigPage />);
    const nav = screen.getByRole("navigation", { name: "Sections" });
    expect(
      within(nav)
        .getAllByRole("link")
        .map((a) => a.getAttribute("href")),
    ).toEqual(["#config-app", "#config-logger", "#config-datastores"]);
    expect(within(section("logger")).getByText("level")).toBeInTheDocument();
    expect(within(section("logger")).getByText("info")).toBeInTheDocument();
  });

  it("says a secret was redacted, and counts it", () => {
    render(<ConfigPage />);
    expect(
      within(section("datastores")).getByText(/secret · redacted/),
    ).toBeInTheDocument();
    expect(screen.getByText(/1 secret redacted/)).toBeInTheDocument();
    expect(screen.getByText(/3 sections, 5 settings/)).toBeInTheDocument();
  });

  it("narrows to what a search matches", async () => {
    render(<ConfigPage />);
    await userEvent.type(
      screen.getByRole("searchbox", { name: "Search the configuration" }),
      "paladin_app",
    );
    expect(screen.queryByRole("region", { name: "logger" })).toBeNull();
    expect(
      within(section("datastores")).getByText("postgres.dsn"),
    ).toBeInTheDocument();
    expect(
      within(section("datastores")).queryByText("postgres.password"),
    ).toBeNull();
  });

  it("copies one section as YAML", async () => {
    render(<ConfigPage />);
    await userEvent.click(
      screen.getByRole("button", { name: "Copy section app" }),
    );
    expect(h.copy).toHaveBeenCalledWith("app:\n  name: paladin-core\n");
  });

  it("shows the YAML as it came, one toggle away", async () => {
    render(<ConfigPage />);
    await userEvent.click(screen.getByRole("button", { name: "YAML" }));
    expect(screen.getByText(/name: paladin-core/)).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Sections" })).toBeNull();
  });

  it("shows a document that is not YAML as text, and says so", () => {
    h.config.config = "app: [unclosed";
    render(<ConfigPage />);
    expect(screen.getByText(/not YAML/)).toBeInTheDocument();
    expect(screen.getByText("app: [unclosed")).toBeInTheDocument();
  });

  it("says why it could not read the configuration", () => {
    h.config.config = null;
    h.config.error = new Error("permission denied");
    render(<ConfigPage />);
    expect(screen.getByText(/could not be read/)).toBeInTheDocument();
    expect(screen.getByText(/platform.admin/)).toBeInTheDocument();
  });
});
