import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// A failed backend or bucket read used to render as a claim about the data:
// "Backend not found." for a backend that may exist, and "No buckets on this
// backend yet." with a count of 0 and an offer to create one.
const h = vi.hoisted(() => ({
  // Every read the page handed useBuckets.
  reads: [] as unknown[],
  backends: {
    backends: [] as Array<Record<string, unknown>>,
    loading: false,
    error: null as string | null,
    fetchBackends: vi.fn(),
  },
  buckets: {
    buckets: [] as Array<Record<string, unknown>>,
    loading: false,
    error: null as string | null,
    refetch: vi.fn(),
    fetched: true,
    createBucket: vi.fn(),
    deleteBucket: vi.fn(),
  },
  tenants: {
    tenants: [] as Array<Record<string, unknown>>,
    fetchTenants: vi.fn(),
  },
}));

vi.mock("next/navigation", async (importOriginal) => ({
  ...(await importOriginal<typeof import("next/navigation")>()),
  useParams: () => ({ backendId: "primary" }),
  usePathname: () => "/storage-backends/primary",
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/lib/connect/client", () => ({
  userSettingsClient: {
    getMine: vi.fn(async () => ({ preferences: {} })),
    updateMine: vi.fn(),
  },
}));
vi.mock("./BackendActions", () => ({ BackendActions: () => null }));
vi.mock("@/hooks/useBackends", () => ({ useBackends: () => h.backends }));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: (read: unknown) => {
    h.reads.push(read);
    return h.buckets;
  },
}));
vi.mock("@/hooks/useTenants", () => ({ useTenants: () => h.tenants }));

import StorageBackendDetailPage from "./page";

// features and compatibility as the server sends them for a backend never probed.
const PRIMARY = {
  backendId: "primary",
  displayName: "Primary",
  kind: 0,
  features: [],
  compatibility: 0,
};

beforeEach(() => {
  h.reads = [];
  h.backends.backends = [PRIMARY];
  h.backends.error = null;
  h.buckets.buckets = [];
  h.buckets.error = null;
});

describe("StorageBackendDetailPage", () => {
  it("says the backend list failed, not that the backend does not exist", () => {
    h.backends.backends = [];
    h.backends.error = "unavailable: upstream";
    render(<StorageBackendDetailPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Backends could not be loaded/,
    );
    expect(screen.queryByText("Backend not found.")).not.toBeInTheDocument();
  });

  it("says the bucket list failed, not that the backend has none", () => {
    h.buckets.error = "unavailable: upstream";
    render(<StorageBackendDetailPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Buckets could not be loaded/,
    );
    expect(
      screen.queryByText("No buckets on this backend yet."),
    ).not.toBeInTheDocument();
    // The count is unknown, not zero.
    expect(
      screen.getByRole("heading", { name: /Buckets on this backend/ }),
    ).toHaveTextContent(/Buckets on this backend\s*—/);
  });

  it("still says so when the backend really has no buckets", () => {
    render(<StorageBackendDetailPage />);
    expect(
      screen.getByText("No buckets on this backend yet."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("StorageBackendDetailPage bucket rows", () => {
  // The backend's list is the same list as the platform's: owner by name and
  // who the bucket is shared with (ADR-0033), not an empty region column.
  it("names each bucket's owner and grantees", () => {
    h.tenants.tenants = [
      { tenantId: "t-owner", slug: "owner", displayName: "Owner Co" },
      { tenantId: "t-a", slug: "tenant-a", displayName: "" },
    ];
    h.buckets.buckets = [
      {
        backendId: "primary",
        bucketId: "pool",
        displayName: "",
        ownerTenantId: "t-owner",
        grantedTenantIds: ["t-a"],
        provisionState: "ready",
      },
    ];
    render(<StorageBackendDetailPage />);
    expect(screen.getByRole("link", { name: "Owner Co" })).toBeInTheDocument();
    expect(screen.getByText("tenant-a")).toBeInTheDocument();
    expect(screen.queryByRole("columnheader", { name: "Region" })).toBeNull();
  });
});

describe("StorageBackendDetailPage bucket list", () => {
  const bucket = (bucketId: string, grantedTenantIds: string[] = []) => ({
    backendId: "primary",
    bucketId,
    displayName: "",
    ownerTenantId: "t-owner",
    grantedTenantIds,
    provisionState: "ready",
  });

  // A list the backend's buckets fill as they grow: searched, narrowed by
  // the facets the platform's list has but the backend, and paged.
  it("narrows by sharing, and searches by name", async () => {
    h.buckets.buckets = [bucket("shared-pool", ["t-a"]), bucket("private")];
    render(<StorageBackendDetailPage />);
    expect(
      screen.queryByRole("button", { name: "Filter by backend" }),
    ).toBeNull();
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by sharing" }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /Owner only/ }),
    );
    await userEvent.keyboard("{Escape}");
    expect(screen.getByText("private")).toBeInTheDocument();
    expect(screen.queryByText("shared-pool")).toBeNull();
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by sharing, 1 chosen" }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /Owner only/ }),
    );
    await userEvent.keyboard("{Escape}");
    await userEvent.type(
      screen.getByRole("searchbox", { name: "Search this backend's buckets" }),
      "shared",
    );
    expect(screen.getByText("shared-pool")).toBeInTheDocument();
    expect(screen.queryByText("private")).toBeNull();
  });
});

describe("StorageBackendDetailPage bucket read", () => {
  // The backend's buckets, asked of the server by backend — not every
  // bucket on every backend, filtered here.
  it("asks the server for this backend's buckets only", () => {
    render(<StorageBackendDetailPage />);
    expect(h.reads.length).toBeGreaterThan(0);
    for (const read of h.reads) expect(read).toEqual({ backendId: "primary" });
  });
});

describe("StorageBackendDetailPage features", () => {
  // The probe is read when a capability is in question, not on every visit:
  // the overview shows its verdict and links to the tab that lists it.
  it("summarises the probe and links to its tab, without listing it", () => {
    h.backends.backends = [
      {
        ...PRIMARY,
        compatibility: 2,
        features: [
          { feature: 1, support: 1, required: true },
          { feature: 2, support: 2, required: false },
        ],
      },
    ];
    render(<StorageBackendDetailPage />);
    const summary = screen.getByRole("link", { name: /1 of 2 supported/ });
    expect(summary).toHaveAttribute(
      "href",
      "/storage-backends/primary/features",
    );
    expect(screen.queryByText("Required by Paladin")).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "S3 features" })).toHaveAttribute(
      "aria-selected",
      "false",
    );
  });
});

// After Argo CD's status panel: a draining backend is a change under way and
// maintenance a pause, neither a fault; a probe that failed is one.
describe("StorageBackendDetailPage status panel", () => {
  const card = async (title: string) => {
    const panel = await screen.findByRole("region", { name: "Backend status" });
    return within(panel)
      .getAllByText(title)[0]
      .closest('[data-slot="status-card"]') as HTMLElement;
  };

  it("reads each fact in the status tones", async () => {
    h.backends.backends = [
      {
        ...PRIMARY,
        enabled: true,
        readOnly: true,
        maintenance: true,
        healthStatus: "error",
        healthMessage: "dial tcp: refused",
      },
    ];
    render(<StorageBackendDetailPage />);
    expect(await card("Lifecycle")).toHaveAttribute("data-tone", "progress");
    const connectivity = await card("Connectivity");
    expect(connectivity).toHaveAttribute("data-tone", "danger");
    expect(connectivity).toHaveTextContent("dial tcp: refused");
    expect(await card("Maintenance")).toHaveAttribute("data-tone", "suspended");
  });

  // The S3 features tab says it in full; a card would only repeat it.
  it("leaves the S3 features to their tab", async () => {
    render(<StorageBackendDetailPage />);
    const panel = await screen.findByRole("region", { name: "Backend status" });
    expect(within(panel).queryByText("S3 features")).toBeNull();
    expect(
      screen.getByRole("tab", { name: "S3 features" }),
    ).toBeInTheDocument();
  });
});
