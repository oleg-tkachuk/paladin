import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { typeToConfirm } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// Characterization net for the cross-tenant collections page. Mock the three
// data hooks + scope + notifications and pin the shell, the on-mount fetch, and
// the provision dialog.
const h = vi.hoisted(() => ({
  fetchCollections: vi.fn((_filter?: string) => Promise.resolve()),
  collections: [] as Array<Record<string, unknown>>,
  options: undefined as unknown,
  // Every filter the page asked the hook for, in order.
  filters: [] as string[],
  createCollection: vi.fn(() => Promise.resolve()),
  deleteCollection: vi.fn(() => Promise.resolve()),
  fetchBuckets: vi.fn(() => Promise.resolve()),
  fetchBackends: vi.fn(),
  showNotification: vi.fn(),
  push: vi.fn(),
  // The page renders ListLoadError; without `error` here that branch never
  // runs and the guard is untested. Same omission as /tenants had.
  error: null as string | null,
}));

// The delete notice opens a collection's trash through the router.
vi.mock("next/navigation", async (importOriginal) => ({
  ...(await importOriginal<typeof import("next/navigation")>()),
  useRouter: () => ({ push: h.push }),
}));
vi.mock("@/hooks/useCollections", () => ({
  useCollections: (options: { filter?: string }) => {
    h.options = options;
    if (h.filters.at(-1) !== options.filter)
      h.filters.push(options.filter ?? "");
    return {
      collections: h.collections,
      nextPageToken: "",
      loading: false,
      error: h.error,
      refetch: h.fetchCollections,
      createCollection: h.createCollection,
      deleteCollection: h.deleteCollection,
    };
  },
}));
vi.mock("@/hooks/useTenants", () => ({
  useTenants: () => ({
    tenants: [
      { tenantId: "t-a", slug: "tenant-a", displayName: "Tenant A" },
      { tenantId: "t-b", slug: "tenant-b", displayName: "" },
    ],
    fetchTenants: vi.fn(),
  }),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({
    buckets: [],
    loading: false,
    fetchBuckets: h.fetchBuckets,
  }),
}));
// The create dialog reads the buckets the acting tenant may bind to.
vi.mock("@/hooks/useActingTenant", () => ({
  useActingTenantId: () => "",
}));
vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({ backends: [], fetchBackends: h.fetchBackends }),
}));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenantId: "t-1", tenant: { displayName: "Acme" } }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({
    title,
    actions,
  }: {
    title: string;
    actions?: React.ReactNode;
  }) => (
    <div>
      <h1>{title}</h1>
      {actions}
    </div>
  ),
}));

import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { ErrorInfoSchema } from "@/gen/google/rpc/error_details_pb";
import { CompletionMode } from "@/gen/paladin/common/v1/resource_pb";
import { PALADIN_ERROR_DOMAIN } from "@/hooks/errorContract";
import CollectionsPage from "./page";

beforeEach(() => {
  h.collections = [];
  h.fetchCollections.mockClear();
  h.createCollection.mockClear();
  h.deleteCollection.mockClear();
  h.showNotification.mockClear();
});

describe("CollectionsPage", () => {
  it("renders the header and a create action", () => {
    render(<CollectionsPage />);
    expect(
      screen.getByRole("heading", { name: "Collections" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /New Collection/i }),
    ).toBeInTheDocument();
  });

  it("opens the provision dialog from the header action", async () => {
    render(<CollectionsPage />);
    await userEvent.click(
      screen.getByRole("button", { name: /New Collection/i }),
    );
    expect(
      screen.getByRole("dialog", { name: "New Collection" }),
    ).toBeInTheDocument();
  });
});

// The search box on this page did not work.
//
// Its contents went straight into `filter`, which the server compiles as CEL,
// so typing "logs" sent the expression `logs` — an undeclared identifier — and
// the plane answered InvalidArgument. The list emptied and the operator saw a
// search that finds nothing. The tenant-scoped collections page carries a
// comment describing this exact failure; nobody carried the fix across.
describe("CollectionsPage search", () => {
  const typeInSearch = (value: string) =>
    act(() => {
      fireEvent.change(screen.getByPlaceholderText(/search/i), {
        target: { value },
      });
    });

  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("sends a CEL expression, not the raw box contents", () => {
    render(<CollectionsPage />);
    h.filters = [];

    typeInSearch("logs");
    act(() => {
      vi.advanceTimersByTime(400);
    });

    const sent = h.filters;
    // The regression: "logs" reaching the plane as a filter.
    expect(sent).not.toContain("logs");
    expect(sent.at(-1)).toBe('search.contains("logs")');
  });

  it("debounces instead of listing once per keystroke", () => {
    render(<CollectionsPage />);
    h.filters = [];

    typeInSearch("l");
    act(() => {
      vi.advanceTimersByTime(100);
    });
    typeInSearch("lo");
    act(() => {
      vi.advanceTimersByTime(100);
    });
    typeInSearch("log");
    act(() => {
      vi.advanceTimersByTime(100);
    });
    expect(h.filters.filter(Boolean)).toHaveLength(0);

    act(() => {
      vi.advanceTimersByTime(400);
    });
    const filtered = h.filters.filter(Boolean);
    expect(filtered).toEqual(['search.contains("log")']);
  });
});

// The ListLoadError branch, which existed and had never run.
describe("CollectionsPage load failure", () => {
  afterEach(() => {
    h.error = null;
  });

  it("says the list is unknown, not empty", () => {
    h.error = "unavailable: connection refused";
    render(<CollectionsPage />);
    expect(
      screen.getByText(
        /could not be loaded — this list is unknown, not empty/i,
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("No collections yet.")).not.toBeInTheDocument();
  });

  it("still says 'none yet' when the list really is empty", () => {
    render(<CollectionsPage />);
    expect(screen.getByText("No collections yet.")).toBeInTheDocument();
  });
});

describe("CollectionsPage catalogue", () => {
  const row = (tenantId: string) => ({
    tenantId,
    collection: "docs",
    displayName: "",
    bucket: "storageBackends/primary/buckets/pool",
    resourceVersion: "1",
  });

  // The platform's catalogue: every tenant's, not the session tenant's.
  it("reads every tenant's collections", () => {
    render(<CollectionsPage />);
    expect(h.options).toMatchObject({ catalogue: true });
  });

  // One name in two tenants is two collections, each its tenant's.
  it("names each row's tenant and links it to that tenant's page", () => {
    h.collections = [row("t-a"), row("t-b")];
    render(<CollectionsPage />);
    expect(screen.getByRole("link", { name: "Tenant A" })).toHaveAttribute(
      "href",
      "/tenants/t-a",
    );
    expect(screen.getByRole("link", { name: "tenant-b" })).toBeInTheDocument();
    const docs = screen.getAllByRole("link", { name: "docs" });
    expect(docs.map((l) => l.getAttribute("href"))).toEqual([
      "/tenants/t-a/collections/docs",
      "/tenants/t-b/collections/docs",
    ]);
  });
});

describe("CollectionsPage bucket column", () => {
  // The bucket by name, linking to its page; the resource name in full had
  // widened the table past the page.
  it("names each row's bucket and links to its page", () => {
    h.collections = [
      {
        tenantId: "t-a",
        collection: "docs",
        displayName: "",
        bucket: "storageBackends/primary/buckets/pool",
        resourceVersion: "1",
      },
    ];
    render(<CollectionsPage />);
    expect(
      screen.getByRole("columnheader", { name: /Bucket/ }),
    ).toBeInTheDocument();
    const link = screen.getByRole("link", { name: "pool" });
    expect(link).toHaveAttribute(
      "href",
      "/storage-backends/primary/buckets/pool",
    );
    expect(link).toHaveAttribute(
      "title",
      "storageBackends/primary/buckets/pool",
    );
  });
});

// Refused because the trash holds it: the notice says so and opens that trash,
// in the tenant the row belongs to.
describe("CollectionsPage delete refused", () => {
  it("offers the trash that holds the collection", async () => {
    h.collections = [
      { collection: "c1", tenantId: "t-a", bucket: "", resourceVersion: "3" },
    ];
    h.deleteCollection.mockRejectedValueOnce(
      new ConnectError(
        "collection still holds 1 object in its trash",
        Code.FailedPrecondition,
        undefined,
        [
          {
            desc: ErrorInfoSchema,
            value: create(ErrorInfoSchema, {
              domain: PALADIN_ERROR_DOMAIN,
              reason: "ERROR_REASON_COLLECTION_NOT_EMPTY",
              metadata: { live_objects: "0", trashed_objects: "1" },
            }),
          },
        ],
      ),
    );
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<CollectionsPage />);
    await user.click(screen.getByRole("button", { name: "Actions for c1" }));
    await user.click(
      await screen.findByRole("menuitem", { name: /Delete Collection/i }),
    );
    await typeToConfirm("c1");
    await user.click(
      await screen.findByRole("button", { name: /^Delete Collection$/ }),
    );
    await vi.waitFor(() => expect(h.showNotification).toHaveBeenCalled());
    const notice = h.showNotification.mock.calls.at(-1)?.[0];
    expect(notice.action.label).toBe("Open its trash");
    notice.action.onClick();
    expect(h.push).toHaveBeenCalledWith("/tenants/t-a/collections/c1/trash");
  });
});

// Every collection in scope is loaded, so the facets count the whole list,
// as every fully loaded list's do, and a choice narrows the rows.
describe("CollectionsPage facets", () => {
  const PRIMARY = "storageBackends/primary/buckets/docs";
  const SECONDARY = "storageBackends/secondary/buckets/media";
  beforeEach(() => {
    window.history.replaceState(null, "", "/collections");
    h.collections = [
      {
        tenantId: "t-a",
        collection: "alpha",
        bucket: PRIMARY,
        completionMode: CompletionMode.IMPLICIT,
        resourceVersion: "1",
      },
      {
        tenantId: "t-a",
        collection: "beta",
        bucket: SECONDARY,
        completionMode: CompletionMode.EXPLICIT,
        resourceVersion: "1",
      },
      {
        tenantId: "t-b",
        collection: "gamma",
        bucket: PRIMARY,
        completionMode: CompletionMode.IMPLICIT,
        resourceVersion: "1",
      },
    ];
  });

  it("narrows to a bucket, by tenant name and by bucket in words", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<CollectionsPage />);
    await user.click(screen.getByRole("button", { name: "Filter by bucket" }));
    await user.click(
      await screen.findByRole("menuitemcheckbox", {
        name: "primary / docs, 2 rows",
      }),
    );
    expect(screen.getByText("alpha")).toBeInTheDocument();
    expect(screen.getByText("gamma")).toBeInTheDocument();
    expect(screen.queryByText("beta")).not.toBeInTheDocument();
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "Filter by tenant" }));
    expect(
      await screen.findByRole("menuitemcheckbox", { name: "Tenant A, 2 rows" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitemcheckbox", { name: "tenant-b, 1 row" }),
    ).toBeInTheDocument();
  });
});
