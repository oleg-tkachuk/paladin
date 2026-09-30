import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
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

import TenantsPage from "./page";

beforeEach(() => {
  h.createTenant.mockReset();
  h.deleteTenant.mockReset();
  h.showNotification.mockReset();
  h.state.tenants = [];
  h.state.loading = false;
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

  it("opens the create dialog from the New tenant button", async () => {
    render(<TenantsPage />);
    await userEvent.click(screen.getByRole("button", { name: /New tenant/i }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByLabelText(/Slug/i)).toBeInTheDocument();
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
    await userEvent.click(screen.getByText("Edit display name"));
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
    // The confirmation alertdialog is now open; its action button confirms.
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
