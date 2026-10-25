import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  fireEvent,
  render,
  screen,
  typeToConfirm,
  waitFor,
  within,
} from "@/test/utils";

import { Code, ConnectError } from "@connectrpc/connect";

import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import { TooltipProvider } from "@/components/ui/Tooltip";

const h = vi.hoisted(() => ({
  blocked: null as string | null,
  restoreObject: vi.fn(),
  purgeObject: vi.fn(),
  bulkDeleteObjects: vi.fn(),
  bulkRestoreObjects: vi.fn(),
  getObject: vi.fn(),
  loadMore: undefined as undefined | (() => void),
  options: [] as Array<{
    filter?: string;
    orderBy?: string;
    sortDirection?: "asc" | "desc" | null;
  }>,
}));

vi.mock("@/hooks/useObjects", () => ({
  useObjects: (options: (typeof h.options)[number]) => (
    h.options.push(options),
    {
      objects: [
        {
          objectId: "o1",
          key: "old.pdf",
          name: "tenants/t-1/collections/docs/objects/old.pdf",
          collection: "docs",
          sizeBytes: 1n,
          state: ObjectState.DELETED,
          tags: {},
        },
      ],
      loading: false,
      loadMore: h.loadMore,
      restoreObject: h.restoreObject,
      purgeObject: h.purgeObject,
      bulkDeleteObjects: h.bulkDeleteObjects,
      bulkRestoreObjects: h.bulkRestoreObjects,
    }
  ),
}));
vi.mock("@/lib/connect/client", () => ({
  objectClient: { getObject: h.getObject },
}));
const COLLECTION_NAME = "tenants/t-1/collections/docs";
vi.mock("../collection-context", () => ({
  useCollection: () => ({
    collection: { collection: "docs", name: COLLECTION_NAME },
  }),
}));
vi.mock("../../../tenant-context", () => ({
  useTenantChangesBlocked: () => h.blocked,
}));

import CollectionTrashPage from "./page";

// Restoring or purging a trashed object changes the tenant, which a tenant in
// the trash refuses.
describe("CollectionTrashPage on a trashed tenant", () => {
  it("holds restore and purge and says why", () => {
    h.blocked = "in the trash";
    render(
      <TooltipProvider>
        <CollectionTrashPage />
      </TooltipProvider>,
    );
    const restore = screen.getByRole("button", { name: /Restore/ });
    expect(restore).toBeDisabled();
    expect(restore).toHaveAttribute("title", "in the trash");
    expect(
      screen.getByRole("button", { name: "Purge old.pdf from storage" }),
    ).toBeDisabled();
  });

  it("leaves them open on a live tenant", () => {
    h.blocked = null;
    render(
      <TooltipProvider>
        <CollectionTrashPage />
      </TooltipProvider>,
    );
    expect(screen.getByRole("button", { name: /Restore/ })).toBeEnabled();
  });
});

function renderPage() {
  render(
    <TooltipProvider>
      <CollectionTrashPage />
    </TooltipProvider>,
  );
}

// Every restore and purge is confirmed first, with a warning; cancelling
// leaves the object where it was.
describe("CollectionTrashPage confirmations", () => {
  beforeEach(() => {
    h.blocked = null;
    for (const fn of [
      h.restoreObject,
      h.purgeObject,
      h.bulkDeleteObjects,
      h.bulkRestoreObjects,
    ])
      fn.mockReset();
  });

  async function openDialog(button: RegExp | string) {
    fireEvent.click(screen.getByRole("button", { name: button }));
    return screen.findByRole("alertdialog");
  }

  it("restores a row only once confirmed", async () => {
    renderPage();
    const dialog = await openDialog(/^Restore$/);
    expect(dialog).toHaveTextContent("old.pdf");
    expect(within(dialog).getByRole("note")).toHaveTextContent(/refused/);
    expect(h.restoreObject).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() =>
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument(),
    );
    expect(h.restoreObject).not.toHaveBeenCalled();

    const again = await openDialog(/^Restore$/);
    fireEvent.click(
      within(again).getByRole("button", { name: "Restore object" }),
    );
    await waitFor(() =>
      expect(h.restoreObject).toHaveBeenCalledWith(
        expect.objectContaining({ objectId: "o1" }),
      ),
    );
    expect(h.bulkRestoreObjects).not.toHaveBeenCalled();
  });

  it("purges a row only once confirmed, warning it is final", async () => {
    renderPage();
    const dialog = await openDialog("Purge old.pdf from storage");
    expect(within(dialog).getByRole("note")).toHaveTextContent(
      /cannot be undone/,
    );
    expect(h.purgeObject).not.toHaveBeenCalled();
    expect(
      within(dialog).getByRole("button", { name: "Purge permanently" }),
    ).toBeDisabled();
    await typeToConfirm("purge");
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Purge permanently" }),
    );
    await waitFor(() => expect(h.purgeObject).toHaveBeenCalledOnce());
  });

  it("confirms restoring and purging the selection through the batch calls", async () => {
    renderPage();
    fireEvent.click(screen.getByRole("checkbox", { name: "Select old.pdf" }));

    const bar = () => within(screen.getByRole("toolbar"));
    fireEvent.click(bar().getByRole("button", { name: /^Restore$/ }));
    const restore = await screen.findByRole("alertdialog");
    expect(restore).toHaveTextContent("Restore this object?");
    expect(h.bulkRestoreObjects).not.toHaveBeenCalled();
    fireEvent.click(
      within(restore).getByRole("button", { name: "Restore object" }),
    );
    await waitFor(() =>
      expect(h.bulkRestoreObjects).toHaveBeenCalledWith([
        expect.objectContaining({ collection: "docs" }),
      ]),
    );
    expect(h.restoreObject).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("checkbox", { name: "Select old.pdf" }));
    fireEvent.click(bar().getByRole("button", { name: /^Purge$/ }));
    const purge = await screen.findByRole("alertdialog");
    await typeToConfirm("purge");
    fireEvent.click(
      within(purge).getByRole("button", { name: "Purge permanently" }),
    );
    await waitFor(() =>
      expect(h.bulkDeleteObjects).toHaveBeenCalledWith(expect.any(Array), {
        permanent: true,
      }),
    );
  });
});

describe("CollectionTrashPage paging and search", () => {
  beforeEach(() => {
    h.options = [];
    h.loadMore = undefined;
  });

  // The search was over the fifty rows loaded; it is now the server's, over
  // every trashed object.
  it("asks the server for trashed objects matching the search", async () => {
    render(
      <TooltipProvider>
        <CollectionTrashPage />
      </TooltipProvider>,
    );
    expect(h.options.at(-1)?.filter).toBe('state == "DELETED"');
    fireEvent.change(screen.getByPlaceholderText(/Search by key/), {
      target: { value: "report" },
    });
    await waitFor(() =>
      expect(h.options.at(-1)?.filter).toBe(
        'state == "DELETED" && key.contains("report")',
      ),
    );
  });

  it("offers more only while there are more", () => {
    const { unmount } = render(
      <TooltipProvider>
        <CollectionTrashPage />
      </TooltipProvider>,
    );
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
    unmount();
    h.loadMore = vi.fn();
    render(
      <TooltipProvider>
        <CollectionTrashPage />
      </TooltipProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(h.loadMore).toHaveBeenCalled();
  });
});

describe("CollectionTrashPage record", () => {
  beforeEach(() => {
    h.blocked = null;
    h.restoreObject.mockReset();
    h.getObject.mockReset();
  });

  it("opens a trashed object's record from its row, into the address", async () => {
    renderPage();
    fireEvent.click(screen.getByRole("row", { name: /o1/ }));
    const panel = await screen.findByRole("dialog", { name: "old.pdf" });
    expect(panel).toHaveAccessibleDescription("o1");
    expect(within(panel).getByText("binary/octet-stream")).toBeInTheDocument();
    expect(window.location.search).toBe("?id=o1");
    // The list holds it, so nothing is read by id.
    expect(h.getObject).not.toHaveBeenCalled();
  });

  it("opens the object a link names on load", async () => {
    window.history.replaceState(null, "", "/?id=o1");
    renderPage();
    expect(
      await screen.findByRole("dialog", { name: "old.pdf" }),
    ).toHaveAccessibleDescription("o1");
    expect(h.getObject).not.toHaveBeenCalled();
  });

  // The trash loads a page at a time: a linked object may not be on it.
  it("reads a linked object the list does not hold by id", async () => {
    h.getObject.mockResolvedValue({
      objectId: "o2",
      key: "archive/later.csv",
      name: `${COLLECTION_NAME}/objects/o2`,
      collection: "docs",
      contentType: "text/csv",
      sizeBytes: 2n,
      state: ObjectState.DELETED,
      tags: {},
    });
    window.history.replaceState(null, "", "/?id=o2");
    renderPage();
    const panel = await screen.findByRole("dialog", { name: "later.csv" });
    expect(within(panel).getByText("text/csv")).toBeInTheDocument();
    expect(h.getObject).toHaveBeenCalledWith(
      { name: `${COLLECTION_NAME}/objects/o2` },
      expect.anything(),
    );

    // Its restore goes through the same confirmation, on the object read.
    fireEvent.click(within(panel).getByRole("button", { name: "Restore" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("archive/later.csv");
    fireEvent.click(
      within(confirm).getByRole("button", { name: "Restore object" }),
    );
    await waitFor(() =>
      expect(h.restoreObject).toHaveBeenCalledWith(
        expect.objectContaining({ objectId: "o2" }),
      ),
    );
  });

  describe("time in the trash", () => {
    const MS_PER_SECOND = 1000;
    const TERMINATED_S = 1_700_000_000;
    const HOUR_S = 3_600;
    const DAY_S = 24 * HOUR_S;
    const IN_TRASH_S = 3 * DAY_S + 4 * HOUR_S;

    // Only Date is faked, so findBy's polling keeps its real timers.
    beforeEach(() => {
      vi.useFakeTimers({ toFake: ["Date"] });
      vi.setSystemTime((TERMINATED_S + IN_TRASH_S) * MS_PER_SECOND);
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    it("counts from its deletion up to now", async () => {
      h.getObject.mockResolvedValue({
        objectId: "o4",
        key: "stale.txt",
        name: `${COLLECTION_NAME}/objects/o4`,
        collection: "docs",
        sizeBytes: 1n,
        state: ObjectState.DELETED,
        terminatedAt: { seconds: BigInt(TERMINATED_S), nanos: 0 },
        tags: {},
      });
      window.history.replaceState(null, "", "/?id=o4");
      renderPage();
      const panel = await screen.findByRole("dialog", { name: "stale.txt" });
      expect(
        within(panel).getByText("In trash for").nextElementSibling?.textContent,
      ).toBe("3 d 4 h");
    });
  });

  it("says so when the linked object is gone", async () => {
    h.getObject.mockRejectedValue(new ConnectError("gone", Code.NotFound));
    window.history.replaceState(null, "", "/?id=o9");
    renderPage();
    const panel = await screen.findByRole("dialog", { name: "Object" });
    expect(
      await within(panel).findByText(/No object with this id is in the trash/),
    ).toBeInTheDocument();
    expect(within(panel).queryByRole("button", { name: "Restore" })).toBeNull();
  });

  it("says so when the linked object was restored", async () => {
    h.getObject.mockResolvedValue({
      objectId: "o3",
      key: "back.txt",
      state: ObjectState.AVAILABLE,
      tags: {},
    });
    window.history.replaceState(null, "", "/?id=o3");
    renderPage();
    expect(
      await screen.findByText(/No object with this id is in the trash/),
    ).toBeInTheDocument();
  });

  it("leaves the row's checkbox and restore to themselves", () => {
    renderPage();
    const row = screen.getByRole("row", { name: /o1/ });
    fireEvent.click(within(row).getByRole("checkbox"));
    fireEvent.click(within(row).getByRole("button", { name: /^Restore$/ }));
    expect(screen.queryByRole("dialog", { name: "old.pdf" })).toBeNull();
  });
});

// The trash loads a page at a time, so it sorts on the server: a header asks
// the list for that order, and the next click for the other way.
describe("CollectionTrashPage sorting", () => {
  beforeEach(() => {
    h.blocked = null;
    h.options = [];
  });

  it("asks the server for the order its headers name", () => {
    render(
      <TooltipProvider>
        <CollectionTrashPage />
      </TooltipProvider>,
    );
    expect(h.options.at(-1)).toMatchObject({ orderBy: undefined });
    const deleted = screen.getByRole("button", { name: /^Deleted at/ });
    fireEvent.click(deleted);
    expect(h.options.at(-1)).toMatchObject({
      orderBy: "terminated_at",
      sortDirection: "asc",
    });
    fireEvent.click(deleted);
    expect(h.options.at(-1)).toMatchObject({
      orderBy: "terminated_at",
      sortDirection: "desc",
    });
    fireEvent.click(screen.getByRole("button", { name: /^Size/ }));
    expect(h.options.at(-1)).toMatchObject({
      orderBy: "size_bytes",
      sortDirection: "asc",
    });
  });
});
