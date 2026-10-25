import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";

import { render, screen, within } from "@/test/utils";

const h = vi.hoisted(() => ({
  config: {
    config: "" as string | null,
    defaults: null as string | null,
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
  h.config.defaults = null;
  h.config.error = null;
  h.config.loading = false;
  h.copy.mockReset().mockResolvedValue(true);
  h.notify.mockReset();
});

const section = (key: string) =>
  screen.getByRole("region", { name: new RegExp(`^${key}`) });

describe("ConfigPage", () => {
  it("lists each section with its settings", () => {
    render(<ConfigPage />);
    expect(
      screen
        .getAllByRole("region")
        .map((r) => r.getAttribute("aria-labelledby")),
    ).toEqual([
      "config-app-title",
      "config-logger-title",
      "config-datastores-title",
    ]);
    expect(within(section("logger")).getByText("level")).toBeInTheDocument();
    expect(within(section("logger")).getByText("info")).toBeInTheDocument();
  });

  it("narrows to one section", async () => {
    render(<ConfigPage />);
    await userEvent.click(screen.getByRole("combobox", { name: "Section" }));
    await userEvent.click(
      await screen.findByRole("option", { name: "logger (2)" }),
    );
    expect(screen.getAllByRole("region")).toHaveLength(1);
    expect(section("logger")).toBeInTheDocument();
  });

  it("hides settings left unset, on request", async () => {
    h.config.config = YAML + "auth:\n  signing_key_secret: null\n";
    render(<ConfigPage />);
    expect(screen.getByRole("region", { name: /auth/ })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("switch", { name: "Hide unset" }));
    expect(screen.queryByRole("region", { name: /auth/ })).toBeNull();
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
    expect(screen.queryByRole("region", { name: /^logger/ })).toBeNull();
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
    expect(screen.queryAllByRole("region")).toHaveLength(0);
  });

  it("diffs the running configuration against one pasted in", async () => {
    render(<ConfigPage />);
    await userEvent.click(screen.getByRole("button", { name: "Compare" }));
    const box = screen.getByLabelText("Compare with");
    await userEvent.click(box);
    // Checked in with a comment, the secret in clear and another level.
    await userEvent.paste(
      "# checked in\n" +
        YAML.replace("'***'", "s3cret").replace("info", "debug"),
    );
    const diff = screen.getByRole("table", {
      name: "Changes from pasted to running",
    });
    expect(diff).toHaveTextContent("level: debug");
    expect(diff).toHaveTextContent("level: info");
    expect(diff).not.toHaveTextContent("s3cret");
    expect(screen.getByText("1 added, 1 removed")).toBeInTheDocument();
  });

  // The defaults come from the server, so Compare opens on what this
  // deployment changed, and a pasted copy is one click away.
  it("diffs the running configuration against the defaults", async () => {
    h.config.defaults = YAML.replace("paladin-core", "''").replace(
      "dsn: postgres://paladin_app@pg/paladin",
      "dsn: ''",
    );
    render(<ConfigPage />);
    await userEvent.click(screen.getByRole("button", { name: "Compare" }));
    expect(
      screen.getByRole("button", { name: "Defaults", pressed: true }),
    ).toBeInTheDocument();
    const diff = screen.getByRole("table", {
      name: "Changes from defaults to running",
    });
    expect(diff).toHaveTextContent("name: paladin-core");
    expect(diff).not.toHaveTextContent("level: info");
    await userEvent.click(
      screen.getByRole("button", { name: "A pasted config.yaml" }),
    );
    expect(screen.getByLabelText("Compare with")).toBeInTheDocument();
  });

  it("offers no defaults a server did not send", async () => {
    render(<ConfigPage />);
    await userEvent.click(screen.getByRole("button", { name: "Compare" }));
    expect(screen.getByRole("button", { name: "Defaults" })).toBeDisabled();
  });

  it("says when the pasted text is not YAML", async () => {
    render(<ConfigPage />);
    await userEvent.click(screen.getByRole("button", { name: "Compare" }));
    await userEvent.click(screen.getByLabelText("Compare with"));
    await userEvent.paste("app: [unclosed");
    expect(
      screen.getByText("The pasted text is not YAML."),
    ).toBeInTheDocument();
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
