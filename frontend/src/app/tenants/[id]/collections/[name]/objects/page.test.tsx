import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

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
  copy: vi.fn(() => Promise.resolve()),
  genUrl: vi.fn(() => Promise.resolve(undefined)),
  replace: vi.fn(),
  showNotification: vi.fn(),
  objects: [] as Array<Record<string, unknown>>,
  // Last options useObjects was called with — lets the filter-wiring tests
  // assert the CEL filter + sort the page derives and feeds the data hook.
  lastOpts: undefined as
    { filter: string; orderBy?: string; sortDirection: unknown } | undefined,
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
      copyObject: h.copy,
      generateDownloadUrl: h.genUrl,
    };
  },
}));
vi.mock("@/hooks/useDistinctTags", () => ({ useDistinctTags: () => [] }));
vi.mock("@/context/ActionsContext", () => ({
  useActions: () => ({
    registerAction: vi.fn(),
    unregisterAction: vi.fn(),
    executeAction: vi.fn(),
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
vi.mock("../collection-context", () => ({
  useCollection: () => ({ collection: { collection: "ok-1", bucket: "b-1" } }),
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
  BulkActionsToolbar: ({ selectedCount }: { selectedCount: number }) => (
    <div data-testid="bulk-toolbar">selected:{selectedCount}</div>
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
        <button
          onClick={() => onCopy({ key: obj.key, collection: obj.collection })}
        >
          copy {obj.key}
        </button>
        <button
          onClick={() => onMove({ key: obj.key, collection: obj.collection })}
        >
          move {obj.key}
        </button>
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
  ] as const) {
    h[k].mockClear();
  }
  h.objects = [makeObj("path/to/file.txt")];
  h.lastOpts = undefined;
  localStorage.clear();
});

describe("CollectionObjectsPage", () => {
  it("renders the Collection scope and the object count", () => {
    render(<CollectionObjectsPage />);
    expect(screen.getByText("ok-1")).toBeInTheDocument();
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
    await waitFor(() => expect(h.replace).toHaveBeenCalled());
    await waitFor(() => expect(h.lastOpts?.orderBy).toBe("key"));
  });

  it("applies a status filter to the CEL filter", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("set status"));
    await waitFor(() => expect(h.lastOpts?.filter).toBe("state == 'active'"));
    expect(h.replace).toHaveBeenCalled();
  });

  it("applies a tag facet to the CEL filter", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("set tag"));
    await waitFor(() =>
      expect(h.lastOpts?.filter).toContain("tags['env'] == 'prod'"),
    );
  });

  it("debounces search into the CEL filter", async () => {
    render(<CollectionObjectsPage />);
    await userEvent.click(screen.getByText("set search"));
    // The visible input updates immediately but the filter trails 300ms.
    await waitFor(
      () => expect(h.lastOpts?.filter).toContain("key.contains('hello')"),
      { timeout: 1500 },
    );
  });
});
