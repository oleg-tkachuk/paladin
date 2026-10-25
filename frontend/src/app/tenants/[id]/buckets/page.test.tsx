import { describe, expect, it, vi, beforeEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  typeToConfirm,
  waitFor,
} from "@/test/utils";
import userEvent from "@testing-library/user-event";

// Characterization net for the tenant buckets page. No decomposition yet — this
// pins the observable behaviors (header + create action, the tenant-scoped
// fetch on mount, the create dialog wiring, and delete via the row menu) so a
// later refactor has a safety floor. Mocks the data hooks + tenant context +
// notifications; useTableSort is a pure hook and stays real.
const h = vi.hoisted(() => ({
  buckets: [] as Array<Record<string, unknown>>,
  bucketsError: null as string | null,
  // Every read the page handed useBuckets.
  reads: [] as unknown[],
  refetch: vi.fn(),
  createBucket: vi.fn(() => Promise.resolve()),
  deleteBucket: vi.fn(() => Promise.resolve()),
  showNotification: vi.fn(),
  storageLayout: "dedicated",
}));

vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: (read: unknown) => {
    h.reads.push(read);
    return {
      buckets: h.buckets,
      loading: false,
      error: h.bucketsError,
      refetch: h.refetch,
      fetched: true,
      createBucket: h.createBucket,
      deleteBucket: h.deleteBucket,
    };
  },
}));
vi.mock("@/hooks/useTenants", () => ({
  useTenants: () => ({ tenants: [], fetchTenants: vi.fn() }),
}));
vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({
    backends: [
      { backendId: "be-1", features: [], enabled: true, declared: true },
    ],
  }),
}));
vi.mock("../tenant-context", () => ({
  useTenantChangesBlocked: () => null,
  useTenant: () => ({
    tenantId: "t-1",
    slug: "acme",
    displayName: "Acme",
    storageLayout: h.storageLayout,
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import TenantBucketsPage from "./page";
import { PROVISION_POLL_MS } from "@/lib/bucketProvision";
import {
  STORAGE_LAYOUT_DEDICATED,
  STORAGE_LAYOUT_SHARED,
} from "@/lib/storageLayout";
import {
  OWNED_BUCKET_LABEL,
  GRANTED_BUCKET_LABEL,
} from "@/components/features/buckets/BucketTenancyBadge";

const TENANT_ID = "t-1";
// ADR-0032: the page asks for the buckets the tenant may bind to — owned plus
// granted — by the tenant's resource name.
const TENANT_NAME = `tenants/${TENANT_ID}`;
/** The tenant that owns the buckets granted to this one. */
const OTHER_OWNER = "t-platform";

const makeBucket = (name: string, ownerTenantId: string = TENANT_ID) => ({
  backendId: "be-1",
  ownerTenantId,
  bucketId: name,
  grantedTenantIds: [] as string[],
  displayName: "",
  region: "us-east-1",
  provisionState: "PROVISION_STATE_READY",
  resourceVersion: "v1",
});

beforeEach(() => {
  h.buckets = [];
  h.bucketsError = null;
  h.storageLayout = STORAGE_LAYOUT_DEDICATED;
  h.reads = [];
  h.refetch.mockClear();
  h.createBucket.mockClear();
  h.deleteBucket.mockClear();
  h.showNotification.mockClear();
});

describe("TenantBucketsPage", () => {
  it("renders the header and a create action", () => {
    render(<TenantBucketsPage />);
    expect(
      screen.getByRole("heading", { name: "Buckets" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /New bucket/i }),
    ).toBeInTheDocument();
  });

  // A shared-layout tenant owns no bucket: asking by owner listed nothing for
  // a tenant whose every collection sits in a bucket granted to it.
  it("reads the buckets this tenant may bind to", () => {
    render(<TenantBucketsPage />);
    expect(h.reads.length).toBeGreaterThan(0);
    for (const read of h.reads) {
      expect(read).toEqual({ accessibleTo: TENANT_NAME });
    }
  });

  // The backend choice was a select of its own; it is a facet now, as on the
  // platform's bucket list, counted over the buckets loaded.
  it("narrows the buckets by backend, counting each", async () => {
    h.buckets = [
      makeBucket("on-one"),
      { ...makeBucket("on-two"), backendId: "be-2" },
    ];
    render(<TenantBucketsPage />);
    expect(screen.getByText("on-two")).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by backend" }),
    );
    const two = screen.getByRole("menuitemcheckbox", { name: /be-2/ });
    expect(two).toHaveTextContent("1");
    await userEvent.click(two);
    await userEvent.keyboard("{Escape}");
    expect(screen.getByText("on-two")).toBeInTheDocument();
    expect(screen.queryByText("on-one")).toBeNull();
  });

  // Refresh called fetchBuckets() bare, which lists every tenant's buckets.
  it("refreshes this tenant's buckets, not everyone's", () => {
    render(<TenantBucketsPage />);
    fireEvent.click(screen.getByRole("button", { name: /refresh/i }));
    // refetch re-reads the page's own read, which is this tenant's.
    expect(h.refetch).toHaveBeenCalledTimes(1);
    expect(h.reads.at(-1)).toEqual({ accessibleTo: TENANT_NAME });
  });

  // A bucket settles in the reconciler; the list asks again until it has.
  it("keeps asking while a bucket is still provisioning", () => {
    vi.useFakeTimers();
    try {
      h.buckets = [{ ...makeBucket("fresh"), provisionState: "pending" }];
      render(<TenantBucketsPage />);
      expect(h.refetch).not.toHaveBeenCalled();
      vi.advanceTimersByTime(PROVISION_POLL_MS);
      expect(h.refetch).toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not poll a settled list", () => {
    vi.useFakeTimers();
    try {
      h.buckets = [{ ...makeBucket("done"), provisionState: "ready" }];
      render(<TenantBucketsPage />);
      vi.advanceTimersByTime(PROVISION_POLL_MS * 2);
      expect(h.refetch).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("opens the create dialog from the header action", async () => {
    render(<TenantBucketsPage />);
    await userEvent.click(screen.getByRole("button", { name: /New bucket/i }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByLabelText(/Bucket name/)).toBeInTheDocument();
  });

  it("does not create when the bucket name is empty", async () => {
    render(<TenantBucketsPage />);
    await userEvent.click(screen.getByRole("button", { name: /New bucket/i }));
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    expect(h.createBucket).not.toHaveBeenCalled();
  });

  it("says a dedicated tenant's new bucket is its own", async () => {
    render(<TenantBucketsPage />);
    await userEvent.click(screen.getByRole("button", { name: /New bucket/i }));
    expect(screen.getByRole("dialog")).toHaveTextContent(/owned by Acme/);
  });

  // ADR-0015: the dedicated tenant owns its bucket. It was created unowned,
  // so it did not appear on the page it was created from.
  it("creates a bucket owned by a dedicated tenant", async () => {
    render(<TenantBucketsPage />);
    await userEvent.click(screen.getByRole("button", { name: /New bucket/i }));
    await userEvent.type(screen.getByLabelText(/Bucket name/), "my-bucket");
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    await waitFor(() =>
      expect(h.createBucket).toHaveBeenCalledWith(
        "be-1",
        "my-bucket",
        "",
        "",
        true,
        null,
        TENANT_ID,
      ),
    );
  });

  it("deletes a bucket from the row menu", async () => {
    h.buckets = [makeBucket("my-bucket")];
    // Radix sets pointer-events:none on the body while the menu/overlay is
    // open; userEvent's default pointer-events check would then refuse the
    // follow-up clicks. Disable it for this open-menu → confirm flow.
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<TenantBucketsPage />);
    await user.click(
      await screen.findByRole("button", { name: "Actions for my-bucket" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: /Delete bucket/i }),
    );
    await typeToConfirm("my-bucket");
    await user.click(
      await screen.findByRole("button", { name: "Delete bucket" }),
    );
    await waitFor(() =>
      expect(h.deleteBucket).toHaveBeenCalledWith(
        "be-1",
        "my-bucket",
        "v1",
        false,
      ),
    );
  });

  it("tells an owned bucket from one granted to the tenant", () => {
    h.buckets = [makeBucket("mine"), makeBucket("common", OTHER_OWNER)];
    render(<TenantBucketsPage />);
    const mine = screen.getByText("mine").closest("tr")!;
    const common = screen.getByText("common").closest("tr")!;
    expect(mine).toHaveTextContent(OWNED_BUCKET_LABEL);
    expect(mine).not.toHaveTextContent(GRANTED_BUCKET_LABEL);
    expect(common).toHaveTextContent(GRANTED_BUCKET_LABEL);
    expect(common).not.toHaveTextContent(OWNED_BUCKET_LABEL);
  });

  // ADR-0033: a bucket has one page, under its backend, owned or granted.
  it("links every bucket to its one page", () => {
    h.buckets = [makeBucket("mine"), makeBucket("common", OTHER_OWNER)];
    render(<TenantBucketsPage />);
    expect(screen.getByRole("link", { name: /mine/ })).toHaveAttribute(
      "href",
      "/storage-backends/be-1/buckets/mine",
    );
    expect(screen.getByRole("link", { name: /common/ })).toHaveAttribute(
      "href",
      "/storage-backends/be-1/buckets/common",
    );
  });

  // A granted bucket is its owner's: its deletion and its lifecycle rules
  // reach every tenant in it, so neither is offered on one tenant's page.
  it("offers no delete or lifecycle rules for a granted bucket", async () => {
    h.buckets = [makeBucket("common", OTHER_OWNER)];
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<TenantBucketsPage />);
    await user.click(
      await screen.findByRole("button", { name: "Actions for common" }),
    );
    expect(
      await screen.findByRole("menuitem", { name: /Open the bucket/i }),
    ).toHaveAttribute("href", "/storage-backends/be-1/buckets/common");
    expect(
      screen.queryByRole("menuitem", { name: /Lifecycle rules/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("menuitem", { name: /Delete bucket/i }),
    ).not.toBeInTheDocument();
  });

  it("offers lifecycle rules for a bucket the tenant owns", async () => {
    h.buckets = [makeBucket("mine")];
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<TenantBucketsPage />);
    await user.click(
      await screen.findByRole("button", { name: "Actions for mine" }),
    );
    expect(
      await screen.findByRole("menuitem", { name: /Lifecycle rules/i }),
    ).toHaveAttribute("href", "/storage-backends/be-1/buckets/mine/lifecycle");
  });

  // A shared tenant owns no bucket; it binds to the ones granted to it, and a
  // grant is made on the bucket's page, reached from /buckets.
  describe("for a shared-layout tenant", () => {
    beforeEach(() => {
      h.storageLayout = STORAGE_LAYOUT_SHARED;
    });

    it("offers no bucket creation", () => {
      render(<TenantBucketsPage />);
      expect(
        screen.queryByRole("button", { name: /New bucket/i }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: /Create the first bucket/i }),
      ).not.toBeInTheDocument();
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    it("links to the bucket index", () => {
      render(<TenantBucketsPage />);
      expect(
        screen.getByRole("link", { name: /All buckets/i }),
      ).toHaveAttribute("href", "/buckets");
    });

    it("sends an empty list to where a bucket is granted", () => {
      render(<TenantBucketsPage />);
      expect(screen.getByText(/may bind to no bucket yet/)).toHaveTextContent(
        /owner grants it from the bucket's page/,
      );
      expect(
        screen.getByRole("link", { name: /Find a bucket to grant/i }),
      ).toHaveAttribute("href", "/buckets");
    });

    // An empty layout is the default, shared.
    it("treats an unset layout as shared", () => {
      h.storageLayout = "";
      render(<TenantBucketsPage />);
      expect(
        screen.queryByRole("button", { name: /New bucket/i }),
      ).not.toBeInTheDocument();
    });
  });

  it("says what an empty list means for a tenant", () => {
    render(<TenantBucketsPage />);
    expect(screen.getByText(/may bind to no bucket yet/)).toBeInTheDocument();
  });

  // A failed read rendered "No buckets owned by this tenant yet." with an
  // offer to create one.
  it("says the bucket list failed, not that the tenant owns none", async () => {
    h.bucketsError = "unavailable: upstream";
    render(<TenantBucketsPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Buckets could not be loaded/,
    );
    expect(
      screen.queryByText(/may bind to no bucket yet/),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    // Retry re-reads the same tenant-scoped list the page loaded.
    expect(h.refetch).toHaveBeenCalledTimes(1);
    expect(h.reads.at(-1)).toEqual({ accessibleTo: TENANT_NAME });
  });
});
