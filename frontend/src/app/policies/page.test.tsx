import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

// Characterization net for the policies page. It orchestrates four admin
// clients + three list hooks + live Cedar validation + a Tabs/TestSuite UI, so
// this pins the stable shell (header, the scope/target controls, the three
// tabs, the editor textarea, and the tenant fetch on mount) rather than every
// RPC. Heavy children (PageHeader, CedarIndicator, TestSuite) are stubbed; the
// clients are mocked but barely exercised because the initial target is empty.
const h = vi.hoisted(() => ({
  fetchTenants: vi.fn(),
  fetchBuckets: vi.fn(),
  fetchObjectKeys: vi.fn(),
  showNotification: vi.fn(),
}));

vi.mock("@/hooks/useTenants", () => ({
  useTenants: () => ({
    tenants: [],
    fetchTenants: h.fetchTenants,
    loading: false,
  }),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({
    buckets: [],
    fetchBuckets: h.fetchBuckets,
    loading: false,
  }),
}));
vi.mock("@/hooks/useObjectKeys", () => ({
  useObjectKeys: () => ({
    objectKeys: [],
    fetchObjectKeys: h.fetchObjectKeys,
    loading: false,
  }),
}));
vi.mock("@/hooks/useCedarValidation", () => ({
  useCedarValidation: () => ({ status: "idle" }),
  hasCedarErrors: () => false,
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: { getBucket: vi.fn() },
  objectKeyClient: { getObjectKey: vi.fn() },
  policyClient: { getEffectivePolicy: vi.fn(), validatePolicy: vi.fn() },
  tenantClient: { getTenant: vi.fn() },
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
  h.fetchObjectKeys.mockClear();
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

  it("fetches the tenant list on mount", () => {
    render(<PoliciesPage />);
    expect(h.fetchTenants).toHaveBeenCalled();
  });

  it("shows the Cedar editor textarea and a disabled reload until a target is picked", () => {
    render(<PoliciesPage />);
    expect(
      screen.getByRole("button", { name: "Reload policy" }),
    ).toBeDisabled();
    // The editor tab is the default — its policy <textarea> is present.
    expect(screen.getAllByRole("textbox").length).toBeGreaterThan(0);
  });
});
