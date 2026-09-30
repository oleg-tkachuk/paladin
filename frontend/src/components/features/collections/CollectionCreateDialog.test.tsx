import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const h = vi.hoisted(() => ({
  buckets: [] as Array<Record<string, unknown>>,
  showNotification: vi.fn(),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({ buckets: h.buckets, fetchBuckets: vi.fn() }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { CollectionCreateDialog } from "./CollectionCreateDialog";

function open(
  backends = ["empty", "full"],
  create = vi.fn().mockResolvedValue({}),
) {
  const onCreated = vi.fn();
  render(
    <CollectionCreateDialog
      open
      onOpenChange={vi.fn()}
      backends={backends}
      bucketsHref="/buckets"
      createCollection={create}
      onCreated={onCreated}
    />,
  );
  return { create, onCreated };
}

const submit = () => screen.getByRole("button", { name: "Create Collection" });

describe("CollectionCreateDialog", () => {
  beforeEach(() => {
    h.buckets = [{ backendId: "full", bucketId: "b1", displayName: "" }];
    h.showNotification.mockReset();
  });

  // An empty backend first in the list used to be the default, which left a
  // dialog that could not be submitted.
  it("defaults to a backend that has a bucket, and picks it", async () => {
    const { create, onCreated } = open();
    fireEvent.change(screen.getByLabelText(/Path/), {
      target: { value: "Invoices/2026" },
    });
    fireEvent.submit(submit().closest("form")!);
    await waitFor(() =>
      expect(create).toHaveBeenCalledWith("invoices/2026", "", "full", "b1"),
    );
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
  });

  it("says what is missing while the submit is held", () => {
    open();
    expect(screen.getByText("Enter a path to continue.")).toBeInTheDocument();
    expect(submit()).toBeDisabled();
  });

  it("points to the buckets page when the backend has none", () => {
    h.buckets = [];
    open(["empty"]);
    expect(screen.getByText(/has no bucket/)).toBeInTheDocument();
    expect(screen.getByText("Pick a bucket to continue.")).toBeInTheDocument();
  });

  it("keeps a failed create open, with the reason", async () => {
    open(undefined, vi.fn().mockRejectedValue(new Error("already exists")));
    fireEvent.change(screen.getByLabelText(/Path/), { target: { value: "x" } });
    fireEvent.submit(submit().closest("form")!);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "already exists",
    );
  });
});
