import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";

import { render, screen, waitFor } from "@/test/utils";
import { ObjectSchema } from "@/gen/paladin/data/v1/types_pb";

const h = vi.hoisted(() => ({
  showNotification: vi.fn(),
  restoreObject: vi.fn(),
  bump: vi.fn(),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
vi.mock("@/lib/connect/client", () => ({
  objectClient: { restoreObject: h.restoreObject },
}));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => h.bump,
}));

import { UNDO_LABEL, UndoTrashProvider, useUndoTrash } from "./useUndoTrash";

// As the trash holds it: DeleteObject answers with the new version.
const trashed = create(ObjectSchema, {
  name: "tenants/t/collections/c/objects/o1",
  key: "docs/report.pdf",
  resourceVersion: "8",
});

function Trasher() {
  const { offerUndo } = useUndoTrash();
  return <button onClick={() => offerUndo(trashed)}>trash</button>;
}

beforeEach(() => {
  h.showNotification.mockReset();
  h.restoreObject.mockReset();
  h.bump.mockReset();
});

describe("useUndoTrash", () => {
  it("restores the trashed object, at its new version, after a confirm", async () => {
    h.restoreObject.mockResolvedValue(trashed);
    const user = userEvent.setup();
    render(
      <UndoTrashProvider>
        <Trasher />
      </UndoTrashProvider>,
    );
    await user.click(screen.getByText("trash"));
    const toast = h.showNotification.mock.calls[0][0];
    expect(toast.message).toMatch(/report\.pdf/);
    expect(toast.action.label).toBe(UNDO_LABEL);

    toast.action.onClick();
    expect(h.restoreObject).not.toHaveBeenCalled();
    await user.click(await screen.findByRole("button", { name: "Restore" }));
    await waitFor(() =>
      expect(h.restoreObject).toHaveBeenCalledWith({
        name: trashed.name,
        resourceVersion: "8",
      }),
    );
    expect(h.bump).toHaveBeenCalledWith("objects");
  });

  it("says why a restore was refused", async () => {
    h.restoreObject.mockRejectedValue(new Error("the object changed"));
    const user = userEvent.setup();
    render(
      <UndoTrashProvider>
        <Trasher />
      </UndoTrashProvider>,
    );
    await user.click(screen.getByText("trash"));
    h.showNotification.mock.calls[0][0].action.onClick();
    await user.click(await screen.findByRole("button", { name: "Restore" }));
    await waitFor(() =>
      expect(h.showNotification).toHaveBeenLastCalledWith(
        expect.objectContaining({
          type: "error",
          message: "the object changed",
        }),
      ),
    );
  });

  it("says only that it is trashed outside the shell", async () => {
    const user = userEvent.setup();
    render(<Trasher />);
    await user.click(screen.getByText("trash"));
    expect(h.showNotification.mock.calls[0][0].action).toBeUndefined();
  });
});
