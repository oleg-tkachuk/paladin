import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Protective net for decomposing ObjectVersionsTab (pure helpers, VersionRow,
// VersionDetailsDialog). Mock the object client + notifications; pin the
// behaviors the extraction must preserve: lazy fetch on activation, the
// row list, the details dialog, the restore confirm flow, and the empty state.
const h = vi.hoisted(() => ({
  list: vi.fn(),
  restore: vi.fn(() => Promise.resolve({})),
  showNotification: vi.fn(),
  onObjectChanged: vi.fn(() => Promise.resolve()),
}));

vi.mock("@/lib/connect/client", () => ({
  objectClient: {
    listObjectVersions: h.list,
    restoreObjectVersion: h.restore,
  },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { ObjectVersionsTab } from "./ObjectVersionsTab";

const makeVersion = (id: string, isCurrent: boolean) => ({
  name: `objects/o1/versions/${id}`,
  versionId: id,
  objectId: "obj-1",
  isCurrent,
  isDeleteMarker: false,
  createdAt: { seconds: 1000n },
  sizeBytes: 100n,
  contentType: "text/plain",
  etag: `etag-${id}`,
  checksum: { algorithm: "SHA256", value: "abcdef0123456789" },
  s3Key: `s3/${id}`,
  metadata: {},
  tags: {},
});

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const object = { name: "objects/o1", resourceVersion: "rv-1" } as any;

beforeEach(() => {
  h.list.mockReset();
  h.list.mockResolvedValue({
    versions: [makeVersion("v2", true), makeVersion("v1", false)],
  });
  h.restore.mockClear();
  h.showNotification.mockClear();
  h.onObjectChanged.mockClear();
});

describe("ObjectVersionsTab", () => {
  it("lazily fetches versions when activated", async () => {
    render(
      <ObjectVersionsTab
        active
        object={object}
        onObjectChanged={h.onObjectChanged}
      />,
    );
    await waitFor(() =>
      expect(h.list).toHaveBeenCalledWith({ parent: "objects/o1" }),
    );
  });

  it("does not fetch while inactive", () => {
    render(
      <ObjectVersionsTab
        active={false}
        object={object}
        onObjectChanged={h.onObjectChanged}
      />,
    );
    expect(h.list).not.toHaveBeenCalled();
  });

  it("renders a row per version with a Restore action on non-current ones", async () => {
    render(
      <ObjectVersionsTab
        active
        object={object}
        onObjectChanged={h.onObjectChanged}
      />,
    );
    await waitFor(() =>
      expect(screen.getAllByRole("button", { name: /View/ })).toHaveLength(2),
    );
    // Only the non-current, non-delete-marker version is restorable.
    expect(screen.getAllByRole("button", { name: /Restore/ })).toHaveLength(1);
  });

  it("opens the version details dialog from View", async () => {
    render(
      <ObjectVersionsTab
        active
        object={object}
        onObjectChanged={h.onObjectChanged}
      />,
    );
    const view = await screen.findAllByRole("button", { name: /View/ });
    await userEvent.click(view[0]);
    expect(await screen.findByText("Version details")).toBeInTheDocument();
  });

  it("restores a non-current version through the confirm modal", async () => {
    render(
      <ObjectVersionsTab
        active
        object={object}
        onObjectChanged={h.onObjectChanged}
      />,
    );
    const restore = await screen.findByRole("button", { name: /Restore/ });
    await userEvent.click(restore);
    expect(await screen.findByText("Restore version?")).toBeInTheDocument();
    // Two "Restore" buttons now (the row + the modal action); the modal's is
    // rendered last in the portal.
    const buttons = screen.getAllByRole("button", { name: /^Restore$/ });
    await userEvent.click(buttons[buttons.length - 1]);
    await waitFor(() =>
      expect(h.restore).toHaveBeenCalledWith({
        name: "objects/o1/versions/v1",
        resourceVersion: "rv-1",
      }),
    );
    await waitFor(() => expect(h.onObjectChanged).toHaveBeenCalled());
  });

  it("shows the empty state when there are no historical versions", async () => {
    h.list.mockResolvedValue({ versions: [makeVersion("v1", true)] });
    render(
      <ObjectVersionsTab
        active
        object={object}
        onObjectChanged={h.onObjectChanged}
      />,
    );
    expect(
      await screen.findByText("No historical versions yet"),
    ).toBeInTheDocument();
  });
});
