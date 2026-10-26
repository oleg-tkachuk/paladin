import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// Protective net for decomposing tenants/page: asserts the list branches +
// the create-dialog open + the create validation guard, so extracting the
// dialogs cannot silently change those behaviors.
const h = vi.hoisted(() => ({
  createTenant: vi.fn(),
  updateTenant: vi.fn(),
  deleteTenant: vi.fn(),
  showNotification: vi.fn(),
  // `error` belongs here because the PAGE reads it — it renders ListLoadError.
  // Without it the double returns undefined, that branch never runs, and a
  // correctly written guard sits untested. Which is how the same branch came
  // to be missing entirely on /storage-backends.
  state: {
    tenants: [] as Array<Record<string, unknown>>,
    loading: false,
    error: null as string | null,
    // What useTenantLimitSignals answers: the page reads only `signals`.
    limits: {
      signals: new Map() as ReadonlyMap<string, TenantLimitSignals>,
      loading: false,
      error: null as string | null,
    },
  },
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/tenants",
  useSearchParams: () => new URLSearchParams(""),
}));
vi.mock("@/hooks/useTenants", () => ({
  useTenants: () => ({
    tenants: h.state.tenants,
    loading: h.state.loading,
    error: h.state.error,
    fetchTenants: vi.fn(),
    createTenant: h.createTenant,
    updateTenantMetadata: h.updateTenant,
    deleteTenant: h.deleteTenant,
  }),
}));
vi.mock("@/hooks/useTenantLimitSignals", () => ({
  useTenantLimitSignals: () => h.state.limits,
}));
vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({ backends: [] }),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({ buckets: [], fetchBuckets: vi.fn() }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({
    title,
    actions,
  }: {
    title?: React.ReactNode;
    actions?: React.ReactNode;
  }) => (
    <div>
      {title}
      {actions}
    </div>
  ),
}));

import { create } from "@bufbuild/protobuf";
import { MoneySchema } from "@/gen/google/type/money_pb";
import { TenantBudgetSchema } from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import type { TenantLimitSignals } from "@/lib/tenant/limits";
import { CREATE_ACTION } from "@/lib/paletteActions";
import { renderWithActions } from "@/test/withActions";
import { TooltipProvider } from "@/components/ui/Tooltip";
import TenantsPage from "./page";

beforeEach(() => {
  h.createTenant.mockReset();
  h.deleteTenant.mockReset();
  h.showNotification.mockReset();
  h.state.tenants = [];
  h.state.loading = false;
  h.state.limits = { signals: new Map(), loading: false, error: null };
});

describe("TenantsPage", () => {
  it("shows the empty state when there are no tenants", () => {
    render(<TenantsPage />);
    expect(screen.getByText("No tenants yet.")).toBeInTheDocument();
  });

  it("renders a row with the tenant slug and display name", () => {
    h.state.tenants = [
      { tenantId: "t-1", slug: "acme", displayName: "Acme Corp", labels: {} },
    ];
    render(<TenantsPage />);
    expect(screen.getByText("acme")).toBeInTheDocument();
    expect(screen.getByText("Acme Corp")).toBeInTheDocument();
    expect(screen.queryByText("No tenants yet.")).not.toBeInTheDocument();
  });

  // The default bucket is read as the tenant, so the list now has one for
  // every tenant; it links to the bucket's one page (ADR-0033).
  it("links a tenant's default bucket to the bucket's page", () => {
    h.state.tenants = [
      {
        tenantId: "t-1",
        slug: "acme",
        displayName: "Acme Corp",
        labels: {},
        defaultBucket: "storageBackends/primary/buckets/pool",
      },
    ];
    render(<TenantsPage />);
    expect(screen.getByRole("link", { name: "pool" })).toHaveAttribute(
      "href",
      "/storage-backends/primary/buckets/pool",
    );
  });

  // Labels are case-sensitive: an upper-cased chip names another label.
  it("shows labels as they are stored", () => {
    h.state.tenants = [
      {
        tenantId: "t-1",
        slug: "acme",
        displayName: "",
        labels: { app: "billing" },
        defaultBucket: "",
      },
    ];
    render(<TenantsPage />);
    const chip = screen.getByText("app=billing");
    expect(chip.className).not.toMatch(/\buppercase\b/);
  });

  // A row is one line: a long name or label is cut, and whole on hover.
  it("keeps a long display name and label whole in the tooltip", () => {
    const long = "example-app 00000000-0000-4000-8000-000000000001";
    h.state.tenants = [
      {
        tenantId: "t-1",
        slug: "example-app",
        displayName: long,
        labels: { managed_by: "paladin-bootstrap" },
        defaultBucket: "",
      },
    ];
    render(<TenantsPage />);
    expect(screen.getByText(long).closest("a")).toHaveAttribute("title", long);
    expect(screen.getByText("managed_by=paladin-bootstrap")).toHaveAttribute(
      "title",
      "managed_by=paladin-bootstrap",
    );
  });

  it("opens the create dialog from the New tenant button", async () => {
    render(<TenantsPage />);
    await userEvent.click(screen.getByRole("button", { name: /New tenant/i }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByLabelText(/Slug/i)).toBeInTheDocument();
  });

  // The palette's New tenant opens the dialog the page's button does.
  it("offers New tenant in the palette, opening the create dialog", async () => {
    const view = renderWithActions(<TenantsPage />);
    expect(view.actionIds()).toContain(CREATE_ACTION.tenant);
    await view.run(CREATE_ACTION.tenant);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    view.unmount();
  });

  it("rejects an invalid slug without calling createTenant", async () => {
    render(<TenantsPage />);
    await userEvent.click(screen.getByRole("button", { name: /New tenant/i }));
    await userEvent.type(screen.getByLabelText(/Slug/i), "Bad_Slug");
    // Submit the form directly — jsdom's button-click→requestSubmit path is
    // unreliable; the validation lives in the form's onSubmit either way.
    const form = screen.getByRole("dialog").querySelector("form")!;
    fireEvent.submit(form);
    expect(h.createTenant).not.toHaveBeenCalled();
    // Said at the field, where it can be fixed, not in a toast.
    expect(screen.getByLabelText(/Slug/i)).toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });

  it("edits a display name through the row menu", async () => {
    h.state.tenants = [
      {
        tenantId: "t-1",
        slug: "acme",
        displayName: "Acme Corp",
        labels: {},
        resourceVersion: "7",
      },
    ];
    render(<TenantsPage />);
    await userEvent.click(
      screen.getByRole("button", { name: /Actions for t-1/i }),
    );
    await userEvent.click(screen.getByText("Edit tenant"));
    const input = await screen.findByLabelText("Display name");
    await userEvent.clear(input);
    await userEvent.type(input, "Acme Renamed");
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    await waitFor(() =>
      expect(h.updateTenant).toHaveBeenCalledWith(
        "t-1",
        "7",
        "Acme Renamed",
        {},
      ),
    );
  });

  // A notice in the labels reads as a glyph beside the slug, not as label
  // badges; the tenant's other labels still show.
  it("marks a tenant that carries a notice", () => {
    h.state.tenants = [
      {
        tenantId: "t-1",
        slug: "acme",
        displayName: "Acme Corp",
        labels: {
          team: "storage",
          "notice.paladin.io/content": "Moving to the new backend",
          "notice.paladin.io/severity": "warning",
        },
        resourceVersion: "7",
      },
    ];
    render(
      <TooltipProvider>
        <TenantsPage />
      </TooltipProvider>,
    );
    expect(
      screen.getByRole("img", { name: "Warning: Moving to the new backend" }),
    ).toBeInTheDocument();
    expect(screen.getByText("team=storage")).toBeInTheDocument();
    expect(screen.queryByText(/notice\.paladin\.io/)).not.toBeInTheDocument();
  });

  it("deletes a tenant through the row menu + confirmation", async () => {
    h.state.tenants = [
      {
        tenantId: "t-1",
        slug: "acme",
        displayName: "Acme Corp",
        labels: {},
        resourceVersion: "7",
      },
    ];
    render(<TenantsPage />);
    await userEvent.click(
      screen.getByRole("button", { name: /Actions for t-1/i }),
    );
    await userEvent.click(screen.getByText("Delete tenant"));
    // The confirmation says what DeleteTenant does — a move to the trash —
    // and warns what that means; it used to describe a cascade only purge
    // performs and call the delete unrecoverable.
    const confirm = screen.getByRole("alertdialog");
    expect(confirm).toHaveTextContent(/moves to the trash/);
    expect(within(confirm).getByRole("note")).toHaveTextContent(/frozen/);
    expect(confirm).not.toHaveTextContent(/database intervention|cascade/i);
    expect(h.deleteTenant).not.toHaveBeenCalled();
    await userEvent.click(
      screen.getByRole("button", { name: "Delete tenant" }),
    );
    // Soft delete must thread the tenant's resource_version (OCC) — passing
    // "" makes DeleteTenant(force=false) fail server-side.
    await waitFor(() =>
      expect(h.deleteTenant).toHaveBeenCalledWith("t-1", "7"),
    );
  });
});

// The ListLoadError branch, which existed and had never run: the double did
// not supply `error`, so every test took the empty-state path.
describe("TenantsPage load failure", () => {
  afterEach(() => {
    h.state.error = null;
  });

  it("says the list is unknown, not empty", () => {
    h.state.error = "unavailable: connection refused";
    render(<TenantsPage />);
    expect(
      screen.getByText(
        /could not be loaded — this list is unknown, not empty/i,
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("No tenants yet.")).not.toBeInTheDocument();
  });

  it("still says 'none yet' when the list really is empty", () => {
    render(<TenantsPage />);
    expect(screen.getByText("No tenants yet.")).toBeInTheDocument();
  });
});

describe("TenantsPage views", () => {
  const tenants = () => [
    {
      tenantId: "t-1",
      slug: "acme",
      displayName: "Acme Corp",
      storageLayout: "dedicated",
      defaultBucket: "storageBackends/primary/buckets/paladin-t-1",
      labels: { team: "storage" },
      resourceVersion: "1",
    },
    {
      tenantId: "t-2",
      slug: "globex",
      displayName: "Globex",
      storageLayout: "shared",
      defaultBucket: "storageBackends/primary/buckets/pool",
      labels: {
        "notice.paladin.io/content": "Read-only until Friday",
        "notice.paladin.io/severity": "critical",
      },
      resourceVersion: "1",
    },
  ];
  const renderPage = () =>
    render(
      <TooltipProvider>
        <TenantsPage />
      </TooltipProvider>,
    );
  beforeEach(() => window.history.replaceState(null, "", "/tenants"));
  afterEach(() => window.history.replaceState(null, "", "/tenants"));

  it("tallies the tenants' states above the list", () => {
    h.state.tenants = tenants();
    renderPage();
    expect(
      screen.getByRole("img", { name: "1 Critical notice · 1 Active" }),
    ).toBeInTheDocument();
  });

  it("lays the tenants out as tiles with links into each", async () => {
    h.state.tenants = tenants();
    renderPage();
    await userEvent.click(screen.getByRole("button", { name: "Tiles" }));
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    const tiles = within(screen.getByRole("list", { name: "Tenants" }));
    expect(tiles.getAllByRole("listitem")).toHaveLength(2);
    expect(tiles.getByRole("link", { name: "Globex" })).toHaveAttribute(
      "href",
      "/tenants/globex",
    );
    expect(tiles.getAllByRole("link", { name: "Quotas" })[0]).toHaveAttribute(
      "href",
      "/tenants/acme/quotas",
    );
    expect(window.location.search).toBe("?view=tiles");
  });

  it("opens in the view a link names, as a summary of the tenants", () => {
    window.history.replaceState(null, "", "/tenants?view=summary");
    h.state.tenants = tenants();
    renderPage();
    const layout = screen
      .getByRole("heading", { name: "Storage layout" })
      .closest("[data-slot=card]") as HTMLElement;
    expect(
      within(layout)
        .getAllByRole("listitem")
        .map((li) => li.textContent),
    ).toEqual(["Dedicated (1)", "Shared (1)"]);
    expect(
      screen.getByRole("heading", { name: "Default backend" }),
    ).toBeInTheDocument();
  });
});

// The limit states come from the platform reads (useTenantLimitSignals),
// joined to the tenants by id; a read still loading or refused leaves the
// tenants as the list reads them.
describe("TenantsPage limit states", () => {
  const usd = (units: bigint) =>
    create(MoneySchema, { currencyCode: "USD", units });
  const budget = (spent: bigint) =>
    create(TenantBudgetSchema, {
      tenantId: "t",
      spent: usd(spent),
      budgetCeiling: { case: "maxBudget", value: usd(100n) },
    });
  const tenants = () =>
    ["acme", "globex", "initech", "umbrella"].map((slug, i) => ({
      tenantId: `t-${i + 1}`,
      slug,
      displayName: `${slug} Inc`,
      labels: {},
      resourceVersion: "1",
    }));
  const limits = (): Map<string, TenantLimitSignals> =>
    new Map<string, TenantLimitSignals>([
      ["t-1", { budget: budget(120n) }],
      ["t-2", { quota: "near-limit" }],
      ["t-3", { quota: "near-limit", budget: budget(85n) }],
      // A tenant the list does not hold is not counted.
      ["t-9", { quota: "at-limit" }],
    ]);
  const renderPage = () =>
    render(
      <TooltipProvider>
        <TenantsPage />
      </TooltipProvider>,
    );
  beforeEach(() => window.history.replaceState(null, "", "/tenants"));
  afterEach(() => window.history.replaceState(null, "", "/tenants"));

  it("counts the tenants over budget and near quota in the status bar", () => {
    h.state.tenants = tenants();
    h.state.limits.signals = limits();
    renderPage();
    expect(
      screen.getByRole("img", {
        name: "1 Over budget · 2 Near quota · 1 Active",
      }),
    ).toBeInTheDocument();
  });

  it("shows each tenant's limit state on its row", () => {
    h.state.tenants = tenants();
    h.state.limits.signals = limits();
    renderPage();
    const row = (slug: string) =>
      screen.getByText(slug).closest("tr") as HTMLElement;
    expect(within(row("acme")).getByText("Over budget")).toBeInTheDocument();
    expect(within(row("globex")).getByText("Near quota")).toBeInTheDocument();
    expect(within(row("initech")).getByText("Near quota")).toBeInTheDocument();
    expect(
      within(row("umbrella")).queryByText(/budget|quota/i),
    ).not.toBeInTheDocument();
  });

  it("shows the limit state on a tile, beside a notice that leads", async () => {
    h.state.tenants = [
      ...tenants().slice(0, 1),
      {
        ...tenants()[1],
        labels: {
          "notice.paladin.io/content": "Read-only until Friday",
          "notice.paladin.io/severity": "critical",
        },
      },
    ];
    h.state.limits.signals = limits();
    renderPage();
    await userEvent.click(screen.getByRole("button", { name: "Tiles" }));
    const tiles = within(screen.getByRole("list", { name: "Tenants" }));
    const [acme, globex] = tiles.getAllByRole("listitem");
    expect(within(acme).getByText("Over budget")).toBeInTheDocument();
    expect(within(globex).getByText("Critical notice")).toBeInTheDocument();
    expect(within(globex).getByText("Near quota")).toBeInTheDocument();
  });

  it("counts the limit states in the summary", () => {
    window.history.replaceState(null, "", "/tenants?view=summary");
    h.state.tenants = tenants();
    h.state.limits.signals = limits();
    renderPage();
    const state = screen
      .getByRole("heading", { name: "State" })
      .closest("[data-slot=card]") as HTMLElement;
    expect(
      within(state)
        .getAllByRole("listitem")
        .map((li) => li.textContent),
    ).toEqual(["Over budget (1)", "Near quota (2)", "Active (1)"]);
  });

  it("lists the tenants without limit states while the reads load", () => {
    h.state.tenants = tenants();
    h.state.limits = { signals: new Map(), loading: true, error: null };
    renderPage();
    expect(screen.getByText("acme")).toBeInTheDocument();
    expect(screen.queryByText("Over budget")).not.toBeInTheDocument();
    expect(screen.getByRole("img", { name: "4 Active" })).toBeInTheDocument();
  });

  it("lists the tenants without limit states when a read is refused", () => {
    h.state.tenants = tenants();
    h.state.limits = {
      signals: new Map(),
      loading: false,
      error: "permission denied",
    };
    renderPage();
    expect(screen.getByText("umbrella")).toBeInTheDocument();
    expect(screen.queryByText(/could not be loaded/i)).not.toBeInTheDocument();
    expect(screen.queryByText("permission denied")).not.toBeInTheDocument();
    expect(screen.getByRole("img", { name: "4 Active" })).toBeInTheDocument();
  });
});

// A tenant's row warms the query its page reads, by the handle its link uses.
describe("TenantsPage row intent", () => {
  it("warms the tenant's detail query when pointed at", async () => {
    const { render: rtlRender } = await import("@testing-library/react");
    const { QueryClient, QueryClientProvider } =
      await import("@tanstack/react-query");
    const { tenantDetailQuery } = await import("@/lib/queries/details");
    h.state.tenants = [
      { tenantId: "t-1", slug: "acme", displayName: "Acme Corp", labels: {} },
    ];
    const client = new QueryClient();
    const prefetch = vi
      .spyOn(client, "prefetchQuery")
      .mockResolvedValue(undefined);
    rtlRender(
      <QueryClientProvider client={client}>
        <TenantsPage />
      </QueryClientProvider>,
    );
    fireEvent.mouseEnter(screen.getByText("acme").closest("tr")!);
    expect(prefetch).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: tenantDetailQuery("acme").queryKey }),
    );
  });
});
