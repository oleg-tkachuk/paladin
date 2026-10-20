import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { render, screen, typeToConfirm, waitFor } from "@/test/utils";

// A failed list fell through to "No Collections yet", inviting the operator to
// create one that may already exist; the toast that said otherwise faded.
const h = vi.hoisted(() => ({
  listCollections: vi.fn(),
  deleteCollection: vi.fn(),
  showNotification: vi.fn(),
  push: vi.fn(),
  changesBlocked: null as string | null,
}));

// The delete notice opens a collection's trash through the router.
vi.mock("next/navigation", async (importOriginal) => ({
  ...(await importOriginal<typeof import("next/navigation")>()),
  useRouter: () => ({ push: h.push }),
}));
vi.mock("@/lib/connect/client", () => ({
  collectionClient: { listCollections: h.listCollections },
}));
vi.mock("../tenant-context", () => ({
  useTenantChangesBlocked: () => h.changesBlocked,
  useTenant: () => ({ tenantId: "t-1", slug: "acme", displayName: "Acme" }),
  useTenantOptional: () => ({ tenantId: "t-1" }),
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({ user: { roles: ["platform.admin"], tenantId: "t-1" } }),
}));
// The list is the real hook's, read through the client mock above; the
// changes are stubbed, so a test sees the arguments a delete is made with.
vi.mock("@/hooks/useCollections", async (importOriginal) => {
  const real = await importOriginal<typeof import("@/hooks/useCollections")>();
  return {
    useCollections: (options?: Parameters<typeof real.useCollections>[0]) => ({
      ...real.useCollections(options),
      createCollection: vi.fn(),
      deleteCollection: h.deleteCollection,
    }),
  };
});
vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({
    backends: [],
    error: null,
    loading: false,
    fetchBackends: vi.fn(),
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { ErrorInfoSchema } from "@/gen/google/rpc/error_details_pb";
import { PALADIN_ERROR_DOMAIN } from "@/hooks/errorContract";
import { CREATE_ACTION } from "@/lib/paletteActions";
import { renderWithActions } from "@/test/withActions";
import TenantCollectionsPage from "./page";
import { CollectionAccess } from "@/gen/paladin/admin/v1/types_pb";

describe("TenantCollectionsPage failed list", () => {
  it("says the list could not be loaded, not that there are none", async () => {
    h.listCollections.mockRejectedValue(new Error("unavailable"));
    render(<TenantCollectionsPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Collections could not be loaded/,
    );
    expect(screen.queryByText(/No Collections yet/)).toBeNull();
  });
});

// The delete used to look the row up again at confirm, in whatever the list
// held then. A search typed just before confirming changes the query, the list
// is empty until it answers, and the delete went out with no version: the
// server refused it as a 400, which the toast then called "must be empty".
describe("TenantCollectionsPage delete", () => {
  it("sends the version of the row that was chosen, whatever the list holds at confirm", async () => {
    const row = {
      collection: "c1",
      displayName: "",
      bucket: "b1",
      resourceVersion: "7",
    };
    // The search's answer is held until the confirm is open: let go sooner,
    // under load, it emptied the list before the row's menu was reached.
    let answerSearch: () => void = () => {};
    const searched = new Promise<void>((resolve) => {
      answerSearch = resolve;
    });
    h.listCollections.mockImplementation(({ filter }: { filter: string }) =>
      filter
        ? searched.then(() => ({ collections: [] }))
        : Promise.resolve({ collections: [row] }),
    );
    h.deleteCollection.mockResolvedValue(undefined);
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<TenantCollectionsPage />);

    // The search is typed first and its 300ms debounce fires while the
    // confirm is open, as in the end-to-end run that caught this.
    await screen.findByRole("button", { name: "Actions for c1" });
    await user.type(screen.getByPlaceholderText(/Search by name/), "zzz");
    await user.click(screen.getByRole("button", { name: "Actions for c1" }));
    await user.click(
      await screen.findByRole("menuitem", { name: /Delete Collection/i }),
    );
    await waitFor(() =>
      expect(h.listCollections).toHaveBeenCalledWith(
        expect.objectContaining({ filter: expect.stringContaining("zzz") }),
        expect.anything(),
      ),
    );
    // The list now holds nothing; the confirm still names the chosen row.
    answerSearch();
    await typeToConfirm("c1");
    await user.click(
      await screen.findByRole("button", { name: /^Delete Collection$/ }),
    );

    await waitFor(() =>
      expect(h.deleteCollection).toHaveBeenCalledWith("c1", "7"),
    );
  });
});

// How long the new search is watched while it has not answered.
const IN_FLIGHT_WINDOW_MS = 500;

// Each search emptied the table for a round trip, which also unmounted an
// open row menu under the cursor; the end-to-end delete test caught the menu
// detaching mid-click.
describe("TenantCollectionsPage search", () => {
  it("keeps the last result on screen while a new search loads", async () => {
    const row = {
      collection: "c1",
      displayName: "",
      bucket: "b1",
      resourceVersion: "7",
    };
    h.listCollections.mockImplementation(({ filter }: { filter: string }) =>
      filter
        ? new Promise(() => {}) // the new search never answers here
        : Promise.resolve({ collections: [row] }),
    );
    const user = userEvent.setup();
    render(<TenantCollectionsPage />);
    await screen.findByRole("button", { name: "Actions for c1" });

    await user.type(screen.getByPlaceholderText(/Search by name/), "c");
    await waitFor(() =>
      expect(h.listCollections).toHaveBeenCalledWith(
        expect.objectContaining({ filter: expect.stringContaining("c") }),
        expect.anything(),
      ),
    );
    // The row must not leave the screen while the new search is in flight.
    await expect(
      waitFor(
        () =>
          expect(
            screen.queryByRole("button", { name: "Actions for c1" }),
          ).toBeNull(),
        { timeout: IN_FLIGHT_WINDOW_MS },
      ),
    ).rejects.toThrow();
  });
});

// ADR-0027: a public Collection is marked wherever it is listed.
describe("TenantCollectionsPage public read", () => {
  it("marks a public Collection and not a private one", async () => {
    h.listCollections.mockResolvedValue({
      collections: [
        {
          collection: "photos",
          displayName: "",
          bucket: "pub",
          resourceVersion: "1",
          access: CollectionAccess.PUBLIC_READ,
        },
        {
          collection: "docs",
          displayName: "",
          bucket: "priv",
          resourceVersion: "1",
          access: CollectionAccess.PRIVATE,
        },
      ],
    });
    render(<TenantCollectionsPage />);
    await screen.findByText("docs");
    expect(screen.getAllByText("public")).toHaveLength(1);
    expect(screen.getByText("public").closest("tr")).toHaveTextContent(
      "photos",
    );
  });
});

// A collection only its trash holds used to fail with "has live objects" and
// nowhere to go; the notice now says so and opens that trash.
describe("TenantCollectionsPage delete refused", () => {
  it("offers the trash that holds the collection", async () => {
    h.listCollections.mockResolvedValue({
      collections: [
        {
          collection: "c1",
          tenantId: "t-1",
          bucket: "b1",
          resourceVersion: "7",
        },
      ],
    });
    h.deleteCollection.mockRejectedValue(
      new ConnectError(
        "collection still holds 1 object in its trash; purge or restore them first",
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
    render(<TenantCollectionsPage />);
    await user.click(
      await screen.findByRole("button", { name: "Actions for c1" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: /Delete Collection/i }),
    );
    await typeToConfirm("c1");
    await user.click(
      await screen.findByRole("button", { name: /^Delete Collection$/ }),
    );

    await waitFor(() => expect(h.showNotification).toHaveBeenCalled());
    const notice = h.showNotification.mock.calls.at(-1)?.[0];
    expect(notice.message).toMatch(/in its trash/);
    expect(notice.action.label).toBe("Open its trash");
    notice.action.onClick();
    expect(h.push).toHaveBeenCalledWith("/tenants/t-1/collections/c1/trash");
  });
});

// The palette offers the create only while the page's own button is enabled.
describe("TenantCollectionsPage palette", () => {
  it("offers New collection, opening the create dialog", async () => {
    h.listCollections.mockResolvedValue({ collections: [] });
    h.changesBlocked = null;
    const view = renderWithActions(<TenantCollectionsPage />);
    expect(view.actionIds()).toContain(CREATE_ACTION.tenantCollection);
    await view.run(CREATE_ACTION.tenantCollection);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    view.unmount();
  });

  it("offers no create while the tenant's changes are blocked", () => {
    h.listCollections.mockResolvedValue({ collections: [] });
    h.changesBlocked = "The tenant is being deleted.";
    const view = renderWithActions(<TenantCollectionsPage />);
    expect(view.actionIds()).not.toContain(CREATE_ACTION.tenantCollection);
    view.unmount();
    h.changesBlocked = null;
  });
});
