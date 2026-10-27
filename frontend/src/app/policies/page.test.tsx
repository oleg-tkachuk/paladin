import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// Characterization net for the policies page. It orchestrates four admin
// clients + three list hooks + live Cedar validation + a Tabs/TestSuite UI, so
// this pins the stable shell (header, the scope/target controls, the three
// tabs, the editor textarea, and the tenant fetch on mount) rather than every
// RPC. Heavy children (PageHeader, CedarIndicator, TestSuite) are stubbed; the
// clients are mocked but barely exercised because the initial target is empty.
const h = vi.hoisted(() => ({
  fetchTenants: vi.fn(),
  fetchBuckets: vi.fn(),
  collectionOptions: [] as unknown[],
  fetchCollections: vi.fn(),
  showNotification: vi.fn(),
  tenantsError: null as string | null,
  tenants: [] as Array<{ tenantId: string; displayName: string }>,
  getTenant: vi.fn(),
  setInheritedPolicy: vi.fn(),
  invalidatePolicyVersions: vi.fn(),
  history: [] as Array<{ resourceName: string; draft?: string }>,
}));

vi.mock("@/hooks/useTenants", () => ({
  useTenants: () => ({
    tenants: h.tenants,
    refetch: h.fetchTenants,
    loading: false,
    error: h.tenantsError,
  }),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({
    buckets: [],
    fetchBuckets: h.fetchBuckets,
    loading: false,
  }),
}));
vi.mock("@/hooks/useCollections", () => ({
  useCollections: (options: unknown) => (
    h.collectionOptions.push(options),
    {
      collections: [],
      refetch: h.fetchCollections,
      loading: false,
    }
  ),
}));
vi.mock("@/hooks/useCedarValidation", () => ({
  useCedarValidation: () => ({ status: "idle" }),
  hasCedarErrors: () => false,
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: { getBucket: vi.fn() },
  collectionClient: { getCollection: vi.fn() },
  policyClient: { getEffectivePolicy: vi.fn(), validatePolicy: vi.fn() },
  tenantClient: {
    getTenant: h.getTenant,
    setInheritedPolicy: h.setInheritedPolicy,
  },
}));
vi.mock("@/components/features/policy/PolicyHistory", () => ({
  PolicyHistory: (props: { resourceName: string; draft?: string }) => (
    h.history.push(props),
    (<p>history of {props.resourceName}</p>)
  ),
}));
vi.mock("@/lib/queries/policyVersions", () => ({
  invalidatePolicyVersions: h.invalidatePolicyVersions,
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({ title }: { title: string }) => <h1>{title}</h1>,
}));
vi.mock("@/components/ui/CedarIndicator", () => ({
  CedarIndicator: () => null,
}));
vi.mock("./_TestSuite", () => ({ TestSuite: () => null }));

import PoliciesPage from "./page";

beforeEach(() => {
  h.fetchTenants.mockClear();
  h.fetchBuckets.mockClear();
  h.fetchCollections.mockClear();
  h.showNotification.mockClear();
});

describe("PoliciesPage", () => {
  it("renders the header and the scope/target controls", () => {
    render(<PoliciesPage />);
    expect(
      screen.getByRole("heading", { name: "Policies" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Scope")).toBeInTheDocument();
    expect(screen.getByText("Target")).toBeInTheDocument();
  });

  it("renders the editor / effective / simulate tabs", () => {
    render(<PoliciesPage />);
    expect(screen.getByRole("tab", { name: /Editor/i })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /Effective/i })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /Simulate/i })).toBeInTheDocument();
  });

  it("shows the Cedar editor textarea and a disabled reload until a target is picked", () => {
    render(<PoliciesPage />);
    expect(
      screen.getByRole("button", { name: "Reload policy" }),
    ).toBeDisabled();
    // The editor tab is the default — its policy <textarea> is present.
    expect(screen.getAllByRole("textbox").length).toBeGreaterThan(0);
  });

  // The target select read a failed tenant list as "No tenants available"
  // and stayed disabled: nothing to attach a policy to.
  it("says the scope's list failed rather than that it has no targets", () => {
    h.tenantsError = "unavailable: upstream";
    render(<PoliciesPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Tenants could not be loaded/,
    );
    h.tenantsError = null;
  });
});

// A failed policy read left the editor holding whatever it had — the previous
// target's text, or nothing — and Save wrote it over the target's real policy.
describe("PoliciesPage failed policy read", () => {
  it("says so, and will not save over a policy it could not read", async () => {
    h.tenants = [{ tenantId: "acme", displayName: "Acme" }];
    h.getTenant.mockRejectedValue(new Error("unavailable"));
    const user = userEvent.setup();
    render(<PoliciesPage />);
    await user.click(screen.getByLabelText("Target"));
    await user.click(screen.getByRole("option", { name: /Acme/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /The current policy could not be loaded/,
    );
    expect(screen.getByRole("button", { name: /^Save/ })).toBeDisabled();
    h.tenants = [];
  });
});

describe("PoliciesPage collection targets", () => {
  // The system-wide editor may target any tenant's Collection.
  it("reads every tenant's collections", () => {
    render(<PoliciesPage />);
    expect(h.collectionOptions).toContainEqual(
      expect.objectContaining({ catalogue: true }),
    );
  });
});

// What Save would write is shown against what is saved before it is pressed.
describe("PoliciesPage unsaved changes", () => {
  it("diffs the draft against the saved policy, and not until it differs", async () => {
    h.tenants = [{ tenantId: "acme", displayName: "Acme" }];
    h.getTenant.mockResolvedValue({
      inheritedCedarPolicy: "permit (principal, action, resource);\n",
      resourceVersion: "7",
    });
    const user = userEvent.setup();
    render(<PoliciesPage />);
    await user.click(screen.getByLabelText("Target"));
    await user.click(screen.getByRole("option", { name: /Acme/ }));
    const editor = await screen.findByDisplayValue(/permit \(principal/);
    expect(screen.queryByText("Unsaved changes")).toBeNull();

    await user.type(editor, "forbid");
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
    const diff = screen.getByRole("table", {
      name: "Changes from saved to draft",
    });
    expect(diff).toHaveTextContent("forbid");
    expect(diff.querySelector('[data-diff="add"]')).not.toBeNull();
    h.tenants = [];
  });
});

// Reload puts the saved policy back in the editor, even when the saved
// version has not moved; a draft that differs is discarded only once the user
// confirms.
describe("PoliciesPage reload", () => {
  const SAVED = "permit (principal, action, resource);\n";
  const cedarEditor = () =>
    screen.getByRole("textbox", { name: /Cedar policy/ });
  async function openAcme() {
    h.tenants = [{ tenantId: "acme", displayName: "Acme" }];
    h.getTenant.mockResolvedValue({
      inheritedCedarPolicy: SAVED,
      resourceVersion: "7",
    });
    const user = userEvent.setup();
    render(<PoliciesPage />);
    await user.click(screen.getByLabelText("Target"));
    await user.click(screen.getByRole("option", { name: /Acme/ }));
    const editor = await screen.findByDisplayValue(/permit \(principal/);
    await user.type(editor, "forbid");
    return { user };
  }

  it("discards the draft once the user confirms", async () => {
    const { user } = await openAcme();
    await user.click(screen.getByRole("button", { name: "Reload policy" }));
    const dialog = await screen.findByRole("alertdialog", {
      name: "Discard unsaved changes?",
    });
    await user.click(
      within(dialog).getByRole("button", { name: "Reload saved policy" }),
    );
    await waitFor(() => expect(cedarEditor()).toHaveValue(SAVED));
    expect(screen.queryByText("Unsaved changes")).toBeNull();
    h.tenants = [];
  });

  it("keeps the draft when the user keeps editing", async () => {
    const { user } = await openAcme();
    const reads = h.getTenant.mock.calls.length;
    await user.click(screen.getByRole("button", { name: "Reload policy" }));
    const dialog = await screen.findByRole("alertdialog", {
      name: "Discard unsaved changes?",
    });
    await user.click(
      within(dialog).getByRole("button", { name: "Keep editing" }),
    );
    expect(cedarEditor()).toHaveValue(SAVED + "forbid");
    expect(h.getTenant.mock.calls.length).toBe(reads);
    h.tenants = [];
  });
});

// The layer's earlier texts sit under the editor, the draft among them, and a
// save reads them again: the save is the newest.
describe("PoliciesPage policy history", () => {
  it("shows the target's history with the draft, and reads it again after a save", async () => {
    h.tenants = [{ tenantId: "acme", displayName: "Acme" }];
    h.getTenant.mockResolvedValue({
      inheritedCedarPolicy: "permit (principal, action, resource);\n",
      resourceVersion: "7",
    });
    h.setInheritedPolicy.mockResolvedValue({ resourceVersion: "8" });
    const user = userEvent.setup();
    render(<PoliciesPage />);
    expect(screen.queryByText(/^history of/)).toBeNull();
    await user.click(screen.getByLabelText("Target"));
    await user.click(screen.getByRole("option", { name: /Acme/ }));
    const editor = await screen.findByDisplayValue(/permit \(principal/);
    expect(screen.getByText("history of tenants/acme")).toBeInTheDocument();

    await user.type(editor, "x");
    expect(h.history.at(-1)).toEqual({
      resourceName: "tenants/acme",
      draft: "permit (principal, action, resource);\nx",
    });
    expect(h.invalidatePolicyVersions).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: /^Save/ }));
    await waitFor(() =>
      expect(h.invalidatePolicyVersions).toHaveBeenCalledWith(
        expect.anything(),
        "tenants/acme",
      ),
    );
    h.tenants = [];
  });
});
