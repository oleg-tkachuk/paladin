import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const h = vi.hoisted(() => ({ showNotification: vi.fn() }));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { BucketDeleteDialog } from "./BucketDeleteDialog";
import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";

const BUCKET = {
  backendId: "primary",
  bucketId: "paladin-a",
  resourceVersion: "7",
  createdOnBackend: true,
} as Bucket;

function dialog(
  deleteBucket = vi.fn().mockResolvedValue(undefined),
  target: Bucket = BUCKET,
) {
  const onClose = vi.fn();
  render(
    <BucketDeleteDialog
      target={target}
      onClose={onClose}
      deleteBucket={deleteBucket}
    />,
  );
  return { deleteBucket, onClose };
}

beforeEach(() => {
  h.showNotification.mockReset();
});

describe("BucketDeleteDialog", () => {
  it("deletes the record only, at the bucket's version, and closes", async () => {
    const { deleteBucket, onClose } = dialog();
    await userEvent.click(
      screen.getByRole("button", { name: "Delete bucket" }),
    );
    expect(deleteBucket).toHaveBeenCalledWith(
      "primary",
      "paladin-a",
      "7",
      false,
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("deletes the S3 bucket too when asked", async () => {
    const { deleteBucket } = dialog();
    await userEvent.click(
      screen.getByRole("checkbox", { name: /physical S3 bucket/ }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Delete bucket" }),
    );
    expect(deleteBucket).toHaveBeenCalledWith(
      "primary",
      "paladin-a",
      "7",
      true,
    );
  });

  it("says why a deletion failed", async () => {
    dialog(vi.fn().mockRejectedValue(new Error("collections still bound")));
    await userEvent.click(
      screen.getByRole("button", { name: "Delete bucket" }),
    );
    await waitFor(() =>
      expect(h.showNotification).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "error",
          message: "collections still bound",
        }),
      ),
    );
  });

  // An adopted bucket holds data Paladin never wrote; the server refuses to
  // delete it on the backend, so the dialog does not offer to.
  it("does not offer to delete a bucket Paladin did not create", async () => {
    const { deleteBucket } = dialog(undefined, {
      ...BUCKET,
      createdOnBackend: false,
    } as Bucket);
    const remote = screen.getByRole("checkbox", { name: /physical S3 bucket/ });
    expect(remote).toBeDisabled();
    expect(
      screen.getByText(/Paladin did not create this bucket/),
    ).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Delete bucket" }),
    );
    expect(deleteBucket).toHaveBeenCalledWith(
      "primary",
      "paladin-a",
      "7",
      false,
    );
  });
});
