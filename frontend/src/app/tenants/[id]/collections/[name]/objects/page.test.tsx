import { afterEach, describe, expect, it, vi, beforeEach } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";

// Protective net for decomposing the Collection objects page. The page is
// orchestration over many hooks + extracted feature components, so mock the
// data hooks, contexts, and navigation, and stub the heavy child components.
// ObjectTableRow is replaced with a minimal row that surfaces the copy/move/
// delete callbacks as buttons — that lets us drive the page-owned dialogs
// (Copy/Move + the soft/hard delete confirm) and the sort header, which are
// exactly the pieces the extraction will move.
const h = vi.hoisted(() => ({
  refresh: vi.fn(),
  loadMore: vi.fn(),
  softDelete: vi.fn(() => Promise.resolve()),
  purge: vi.fn(() => Promise.resolve()),
  bulkDelete: vi.fn(() => Promise.resolve()),
  bulkRestore: vi.fn(() => Promise.resolve()),
  bulkPatch: vi.fn(() => Promise.resolve()),
  bulkCopy: vi.fn(() => Promise.resolve()),
  copy: vi.fn(() => Promise.resolve()),
  genUrl: vi.fn(() => Promise.resolve(undefined)),
  replace: vi.fn(),
  showNotification: vi.fn(),
  registerAction: vi.fn(),
  uploadFile: vi.fn(() => Promise.resolve()),
  // The columns the user hid, as their table preferences hold them.
  hidden: [] as string[],
  // The reason the tenant takes no changes, or null.
  blocked: null as string | null,
  // The collection's access: 2 is COLLECTION_ACCESS_PUBLIC_READ.
  access: 0,
  objects: [] as Array<Record<string, unknown>>,
  folders: [] as string[],
  // Last options useObjects was called with — lets the filter-wiring tests
  // assert the CEL filter + sort the page derives and feeds the data hook.
  lastOpts: undefined as
    | {
        filter: string;
        orderBy?: string;
        sortDirection: unknown;
        prefix?: string;
        delimiter?: string;
        refetchIntervalMs?: number;
      }
    | undefined,
}));

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(""),
  useRouter: () => ({ replace: h.replace }),
  usePathname: () => "/tenants/t-1/collections/ok-1/objects",
}));
vi.mock("@/hooks/useObjects", () => ({
  useObjects: (opts: {
    filter: string;
    orderBy?: string;
    sortDirection: unknown;
  }) => {
    h.lastOpts = opts;
    return {
      objects: h.objects,
      folders: h.folders,
      foldersTruncated: false,
      loading: false,
      error: null,
      refresh: h.refresh,
      loadMore: h.loadMore,
      nextCursor: undefined,
      softDeleteObject: h.softDelete,
      purgeObject: h.purge,
      bulkDeleteObjects: h.bulkDelete,
      bulkRestoreObjects: h.bulkRestore,
      bulkPatchObjects: h.bulkPatch,
      bulkCopyObjects: h.bulkCopy,
      copyObject: h.copy,
      generateDownloadUrl: h.genUrl,
    };
  },
}));
vi.mock("@/hooks/useDistinctTags", () => ({ useDistinctTags: () => [] }));
vi.mock("@/context/ActionsContext", () => ({
  useActions: () => ({
    registerAction: h.registerAction,
    unregisterAction: vi.fn(),
    executeAction: vi.fn(),
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
vi.mock("@/hooks/useCollections", () => ({
  useCollections: () => ({
    collections: [
      { collection: "ok-1", displayName: "" },
      { collection: "archive", displayName: "" },
    ],
    fetchAllCollections: vi.fn(() => Promise.resolve()),
  }),
}));
vi.mock("@/hooks/useTablePrefs", () => ({
  useTablePrefs: () => ({
    prefs: { hidden: h.hidden },
    toggleColumn: vi.fn(),
    setDensity: vi.fn(),
    saveSort: vi.fn(),
    clearSort: vi.fn(),
  }),
}));
vi.mock("@/hooks/useUpload", () => ({
  useUpload: () => ({
    queue: [],
    uploadFile: h.uploadFile,
    clearQueue: vi.fn(),
  }),
}));
vi.mock("../../../tenant-context", () => ({
  useTenantChangesBlocked: () => h.blocked,
}));
vi.mock("../collection-context", () => ({
  useCollection: () => ({
    collection: { collection: "ok-1", bucket: "b-1", access: h.access },
  }),
}));

// Stub the heavy feature children — not under test here, and they drag in
// their own deps. The page's own table/dialogs stay real.
vi.mock("@/components/features/ObjectInspector", () => ({
  ObjectInspector: () => null,
}));
vi.mock("@/components/features/objects/ObjectsFilterBar", () => ({
  ObjectsFilterBar: ({
    onFilterChange,
  }: {
    onFilterChange: (patch: {
      search?: string;
      status?: string | undefined;
      tag?: string | undefined;
      type?: string | undefined;
      meta?: string | undefined;
      recursive?: boolean;
    }) => void;
  }) => (
    <div>
      <button onClick={() => onFilterChange({ status: "active" })}>
        set status
      </button>
      <button onClick={() => onFilterChange({ tag: "env=prod" })}>
        set tag
      </button>
      <button onClick={() => onFilterChange({ type: "pdf" })}>set type</button>
      <button onClick={() => onFilterChange({ meta: "owner=ops" })}>
        set meta
      </button>
      <button onClick={() => onFilterChange({ search: "hello" })}>
        set search
      </button>
      <button onClick={() => onFilterChange({ recursive: true })}>
        set recursive
      </button>
    </div>
  ),
}));
vi.mock("@/components/features/objects/BulkActionsToolbar", () => ({
  BulkActionsToolbar: ({
    selectedCount,
    onBulkDelete,
    onBulkRestore,
    onBulkCopy,
  }: {
    selectedCount: number;
    onBulkDelete: () => void;
    onBulkRestore?: () => void;
    onBulkCopy?: () => void;
  }) => (
    <div data-testid="bulk-toolbar">
      selected:{selectedCount}
      <button onClick={onBulkDelete}>bulk delete</button>
      {onBulkRestore && <button onClick={onBulkRestore}>bulk restore</button>}
      {onBulkCopy && <button onClick={onBulkCopy}>bulk copy</button>}
    </div>
  ),
}));
vi.mock("@/components/features/objects/SaveViewModal", () => ({
  SaveViewModal: () => null,
}));
vi.mock("@/components/features/objects/BulkEditModal", () => ({
  BulkEditModal: () => null,
}));
vi.mock("@/components/features/objects/ObjectTableRow", () => ({
  ObjectTableRow: ({
    obj,
    onCopy,
    onMove,
    onSoftDelete,
    onHardDelete,
  }: {
    obj: { objectId: string; collection: string; key: string };
    onCopy: (o: { key: string; collection: string }) => void;
    onMove: (o: { key: string; collection: string }) => void;
    onSoftDelete: (o: {
      objectId: string;
      collection: string;
      key: string;
    }) => void;
    onHardDelete: (o: {
      objectId: string;
      collection: string;
      key: string;
    }) => void;
  }) => (
    <tr>
      <td>{obj.key}</td>
      <td>
        {onCopy && (
          <button
            onClick={() => onCopy({ key: obj.key, collection: obj.collection })}
          >
            copy {obj.key}
          </button>
        )}
        {onMove && (
          <button
            onClick={() => onMove({ key: obj.key, collection: obj.collection })}
          >
            move {obj.key}
          </button>
        )}
        {onSoftDelete && (
          <button
            onClick={() =>
              onSoftDelete({
                objectId: obj.objectId,
                collection: obj.collection,
                key: obj.key,
              })
            }
          >
            trash {obj.key}
          </button>
        )}
        <button
          onClick={() =>
            onHardDelete({
              objectId: obj.objectId,
              collection: obj.collection,
              key: obj.key,
            })
          }
        >
          purge {obj.key}
        </button>
      </td>
    </tr>
  ),
}));

import CollectionObjectsPage from "./page";
import { CollectionAccess } from "@/gen/paladin/admin/v1/types_pb";

const makeObj = (key: string) => ({
  objectId: "o1",
  name: `objects/o1`,
  collection: "ok-1",
  key,
  tags: {},
});

beforeEach(() => {
  for (const k of [
    "refresh",
    "loadMore",
    "softDelete",
    "purge",
    "bulkDelete",
    "bulkRestore",
    "bulkPatch",
    "copy",
    "genUrl",
    "replace",
    "showNotification",
    "registerAction",
  ] as const) {
    h[k].mockClear();
  }
  h.objects = [makeObj("path/to/file.txt")];
  h.access = 0;
  h.lastOpts = undefined;
  localStorage.clear();
});

describe("CollectionObjectsPage", () => {
  it("renders the Collection scope and the object count", () => {
    render(<CollectionObjectsPage />);
    expect(screen.getAllByText("ok-1").length).toBeGreaterThan(0);
    expect(screen.getByText("1 objects")).toBeInTheDocument();
  });

  it("opens the copy dialog and copies on confirm", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("copy path/to/file.txt"));
    expect(
      await screen.findByRole("heading", { name: "Copy object" }),
    ).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: /Confirm copy/i }),
    );
    await waitFor(() => expect(h.copy).toHaveBeenCalled());
    expect(h.copy).toHaveBeenCalledWith(
      "objects/o1",
      expect.any(String),
      "ok-1",
    );
  });

  it("opens the move dialog and copies then soft-deletes on confirm", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("move path/to/file.txt"));
    expect(
      await screen.findByRole("heading", { name: "Move object" }),
    ).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: /Confirm move/i }),
    );
    await waitFor(() => expect(h.copy).toHaveBeenCalled());
    await waitFor(() => expect(h.softDelete).toHaveBeenCalled());
  });

  it("soft-deletes through the confirm dialog", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("trash path/to/file.txt"));
    expect(await screen.findByRole("alertdialog")).toHaveTextContent(
      "Move to Trash",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Move to Trash" }),
    );
    await waitFor(() => expect(h.softDelete).toHaveBeenCalled());
  });

  it("hard-deletes through the confirm dialog", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("purge path/to/file.txt"));
    expect(
      await screen.findByText("Permanently delete object?"),
    ).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Purge permanently" }),
    );
    await waitFor(() => expect(h.purge).toHaveBeenCalled());
  });

  it("syncs the URL and feeds the sort column to the data hook", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Name \/ ID/i }));
    await waitFor(() => expect(h.lastOpts?.orderBy).toBe("key"));
    expect(window.location.search).toContain("sort=key&dir=asc");
  });

  it("applies a status filter to the CEL filter", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("set status"));
    await waitFor(() => expect(h.lastOpts?.filter).toBe('state == "active"'));
    expect(window.location.search).toContain("status=active");
  });

  it("applies a tag facet to the CEL filter", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("set tag"));
    await waitFor(() =>
      expect(h.lastOpts?.filter).toContain('tags["env"] == "prod"'),
    );
  });

  it("applies content type and metadata to the CEL filter, URL-synced", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("set type"));
    await userEvent.click(screen.getByText("set meta"));
    await waitFor(() =>
      expect(h.lastOpts?.filter).toBe(
        'content_type == "application/pdf" && metadata["owner"] == "ops"',
      ),
    );
    expect(window.location.search).toContain("meta=owner%3Dops");
  });

  it("debounces search into the CEL filter", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("set search"));
    // The visible input updates immediately but the filter trails 300ms.
    await waitFor(
      () => expect(h.lastOpts?.filter).toContain('key.contains("hello")'),
      { timeout: 1500 },
    );
  });
});

// Trashing a selection is confirmed first, and so is its Undo: that is a
// restore, which is never run on a click.
describe("CollectionObjectsPage bulk trash", () => {
  it("asks before trashing the selection", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("bulk delete"));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByRole("note")).toHaveTextContent(/restorable/);
    expect(h.bulkDelete).not.toHaveBeenCalled();
    await userEvent.click(
      within(dialog).getByRole("button", { name: /Move 0 to Trash/ }),
    );
    await waitFor(() => expect(h.bulkDelete).toHaveBeenCalledWith([]));
  });

  it("does not trash when cancelled", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("bulk delete"));
    await userEvent.click(
      await screen.findByRole("button", { name: "Cancel" }),
    );
    expect(h.bulkDelete).not.toHaveBeenCalled();
  });

  it("confirms the Undo before restoring", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("bulk delete"));
    await userEvent.click(
      await screen.findByRole("button", { name: /Move 0 to Trash/ }),
    );
    await waitFor(() => expect(h.bulkDelete).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument(),
    );
    const undo = h.registerAction.mock.calls
      .map(([a]) => a as { id: string; perform: () => unknown })
      .find((a) => a.id === "objects.bulk_restore_last");
    expect(undo).toBeDefined();

    act(() => void undo!.perform());
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Restore 0 objects?");
    expect(h.bulkRestore).not.toHaveBeenCalled();
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Restore 0 objects" }),
    );
    await waitFor(() => expect(h.bulkRestore).toHaveBeenCalledWith([]));
  });
});

// ADR-0027: a public collection has no trash and names its objects, so the
// page offers neither a soft delete nor copy/move, and a bulk delete is
// permanent — confirmed first, since nothing undoes it.
describe("CollectionObjectsPage in a public collection", () => {
  beforeEach(() => {
    h.access = CollectionAccess.PUBLIC_READ;
  });

  it("offers only a permanent delete per object", () => {
    render(<CollectionObjectsPage />);
    expect(screen.getByText("purge path/to/file.txt")).toBeInTheDocument();
    expect(
      screen.queryByText("trash path/to/file.txt"),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("copy path/to/file.txt")).not.toBeInTheDocument();
    expect(screen.queryByText("move path/to/file.txt")).not.toBeInTheDocument();
  });

  it("deletes in bulk permanently, after a confirm", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("bulk delete"));
    expect(h.bulkDelete).not.toHaveBeenCalled();
    await userEvent.click(
      screen.getByRole("button", { name: "Delete permanently" }),
    );
    await waitFor(() =>
      expect(h.bulkDelete).toHaveBeenCalledWith([], { permanent: true }),
    );
  });
});

// Files dropped on the list land in the collection the page shows.
describe("CollectionObjectsPage upload", () => {
  beforeEach(() => {
    h.access = 0;
    h.blocked = null;
    h.uploadFile.mockClear();
  });

  const file = new File(["x"], "a.txt", { type: "text/plain" });
  const drag = { dataTransfer: { types: ["Files"], files: [file] } };

  it("uploads dropped files to this collection", () => {
    render(<CollectionObjectsPage />);
    const zone = document.querySelector("[data-drop-zone]") as HTMLElement;
    fireEvent.dragEnter(zone, drag);
    expect(screen.getByText(/Drop to upload to ok-1/)).toBeInTheDocument();
    fireEvent.drop(zone, drag);
    expect(h.uploadFile).toHaveBeenCalledWith(file, "ok-1", {}, "");
  });

  it("takes no files while the tenant takes no changes", () => {
    h.blocked = "The tenant is in the trash.";
    render(<CollectionObjectsPage />);
    expect(screen.getByRole("button", { name: /^Upload$/ })).toBeDisabled();
    const zone = document.querySelector("[data-drop-zone]") as HTMLElement;
    fireEvent.drop(zone, drag);
    expect(h.uploadFile).not.toHaveBeenCalled();
  });
});

// Restore is offered for a selection that is all in the trash, and goes
// through the confirm every restore takes; copy asks where, then runs.
describe("CollectionObjectsPage bulk restore and copy", () => {
  beforeEach(() => {
    h.access = 0;
    h.bulkCopy.mockClear();
  });

  const select = async () =>
    userEvent.click(screen.getByRole("checkbox", { name: "Select all rows" }));

  it("offers restore only when every selected object is trashed", async () => {
    h.objects = [{ ...makeObj("live.txt"), state: ObjectState.AVAILABLE }];
    const { unmount } = render(<CollectionObjectsPage />);
    await select();
    expect(screen.queryByText("bulk restore")).toBeNull();
    unmount();

    h.objects = [{ ...makeObj("gone.txt"), state: ObjectState.DELETED }];
    render(<CollectionObjectsPage />);
    await select();
    await userEvent.click(screen.getByText("bulk restore"));
    expect(h.bulkRestore).not.toHaveBeenCalled();
    await userEvent.click(
      screen.getByRole("button", { name: /^Restore 1 objects$/ }),
    );
    await waitFor(() =>
      expect(h.bulkRestore).toHaveBeenCalledWith([
        { name: "objects/o1", collection: "ok-1" },
      ]),
    );
  });

  it("copies the selection into the chosen collection under a prefix", async () => {
    h.objects = [{ ...makeObj("a.txt"), state: ObjectState.AVAILABLE }];
    render(<CollectionObjectsPage />);
    await select();
    await userEvent.click(screen.getByText("bulk copy"));
    const confirm = screen.getByRole("button", { name: "Copy 1 objects" });
    // Into its own collection with no prefix, each copy would be its source.
    expect(confirm).toBeDisabled();
    await userEvent.type(screen.getByLabelText("Key prefix"), "v2/");
    await userEvent.click(confirm);
    await waitFor(() =>
      expect(h.bulkCopy).toHaveBeenCalledWith(
        [{ name: "objects/o1", collection: "ok-1" }],
        "ok-1",
        "v2/",
      ),
    );
  });
});

// One folder level at a time: the folders show above the objects, open on a
// click, and the trail leads back; Recursive lists everything under it.
describe("CollectionObjectsPage folders", () => {
  beforeEach(() => {
    h.access = 0;
    h.blocked = null;
    h.folders = [];
    h.uploadFile.mockClear();
  });

  it("lists one level, opens a folder, and goes back by the trail", async () => {
    h.folders = ["reports/"];
    render(<CollectionObjectsPage />);
    expect(h.lastOpts).toMatchObject({ prefix: "", delimiter: "/" });
    await userEvent.click(
      screen.getByRole("button", { name: "Open folder reports" }),
    );
    await waitFor(() => expect(h.lastOpts?.prefix).toBe("reports/"));
    expect(window.location.search).toContain("prefix=reports%2F");

    // A file dropped here lands in the folder on screen.
    const file = new File(["x"], "q3.pdf");
    fireEvent.drop(document.querySelector("[data-drop-zone]") as HTMLElement, {
      dataTransfer: { types: ["Files"], files: [file] },
    });
    expect(h.uploadFile).toHaveBeenCalledWith(file, "ok-1", {}, "reports/");

    await userEvent.click(
      within(screen.getByRole("navigation", { name: "Folder" })).getByRole(
        "button",
        {
          name: "ok-1",
        },
      ),
    );
    await waitFor(() => expect(h.lastOpts?.prefix).toBe(""));
  });

  it("lists every level under the folder when Recursive is on", async () => {
    window.history.replaceState(
      null,
      "",
      "/x?prefix=reports%2F&recursive=true",
    );
    render(<CollectionObjectsPage />);
    expect(h.lastOpts).toMatchObject({ prefix: "reports/", delimiter: "" });
    expect(screen.queryByRole("button", { name: /Open folder/ })).toBeNull();
  });
});

// The list holds still until Live is switched on, then asks again on a timer.
describe("CollectionObjectsPage live", () => {
  it("is paused until switched on", async () => {
    render(<CollectionObjectsPage />);
    const live = screen.getByRole("button", { name: "Live" });
    expect(live).toHaveAttribute("aria-pressed", "false");
    expect(h.lastOpts?.refetchIntervalMs).toBeUndefined();
    await userEvent.click(live);
    await waitFor(() =>
      expect(h.lastOpts?.refetchIntervalMs).toBeGreaterThan(0),
    );
  });
});

// A column the user hid stays hidden: it is their table preference.
describe("CollectionObjectsPage columns", () => {
  afterEach(() => {
    h.hidden = [];
  });

  it("leaves out the columns the user hid", () => {
    h.objects = [makeObj("a.txt")];
    h.hidden = ["mime"];
    render(<CollectionObjectsPage />);
    expect(screen.queryByRole("button", { name: /^MIME/ })).toBeNull();
    expect(screen.getByRole("button", { name: /^Size/ })).toBeInTheDocument();
  });
});
