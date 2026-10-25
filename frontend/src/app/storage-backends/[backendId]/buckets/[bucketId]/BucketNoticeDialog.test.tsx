import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { create } from "@bufbuild/protobuf";

import { render, screen, waitFor } from "@/test/utils";
import { BucketSchema } from "@/gen/paladin/admin/v1/types_pb";
import { noticeFromLabels } from "@/lib/notice";
import { labelsWithNoticeForm } from "@/lib/noticeForm";

const h = vi.hoisted(() => ({
  updateBucket: vi.fn(),
  notify: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: { updateBucket: h.updateBucket },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));

import { BucketNoticeDialog } from "./BucketNoticeDialog";

/** A label the bucket carries that is not its notice's. */
const OTHER_LABEL = { team: "storage" };

const bucketWith = (labels: Record<string, string>) =>
  create(BucketSchema, {
    name: "storageBackends/primary/buckets/pool",
    bucketId: "pool",
    resourceVersion: "7",
    labels,
  });

function open(labels: Record<string, string>, onSaved = vi.fn()) {
  render(
    <BucketNoticeDialog
      bucket={bucketWith(labels)}
      open
      onClose={vi.fn()}
      onSaved={onSaved}
    />,
  );
  return onSaved;
}

const save = () => screen.getByRole("button", { name: "Save notice" });

beforeEach(() => {
  h.updateBucket.mockReset();
  h.notify.mockReset();
});

describe("BucketNoticeDialog", () => {
  it("writes the notice's labels under the bucket's version, keeping the others", async () => {
    h.updateBucket.mockImplementation(async (req) => req.bucket);
    const onSaved = open(OTHER_LABEL);
    expect(save()).toBeDisabled();
    await userEvent.type(
      screen.getByLabelText("Notice"),
      "Read-only while it moves.",
    );
    await userEvent.click(save());
    await waitFor(() => expect(h.updateBucket).toHaveBeenCalled());
    const req = h.updateBucket.mock.calls[0][0];
    expect(req).toMatchObject({
      name: "storageBackends/primary/buckets/pool",
      resourceVersion: "7",
      updateMask: { paths: ["labels"] },
    });
    expect(req.bucket.labels).toMatchObject(OTHER_LABEL);
    expect(noticeFromLabels(req.bucket.labels)?.content).toBe(
      "Read-only while it moves.",
    );
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
  });

  it("opens on the notice the bucket carries, and clears it", async () => {
    h.updateBucket.mockImplementation(async (req) => req.bucket);
    const carried = labelsWithNoticeForm(OTHER_LABEL, {
      notice: "Moving",
      severity: "warning",
      url: "",
      permanent: false,
    });
    open(carried);
    expect(screen.getByLabelText("Notice")).toHaveValue("Moving");
    await userEvent.clear(screen.getByLabelText("Notice"));
    await userEvent.click(save());
    await waitFor(() => expect(h.updateBucket).toHaveBeenCalled());
    expect(h.updateBucket.mock.calls[0][0].bucket.labels).toEqual(OTHER_LABEL);
  });

  it("holds a link it could not show", async () => {
    open({});
    await userEvent.type(screen.getByLabelText("Notice"), "See the plan");
    await userEvent.type(screen.getByLabelText("Link"), "javascript:alert(1)");
    expect(save()).toBeDisabled();
  });

  it("says why the save failed", async () => {
    h.updateBucket.mockRejectedValue(new Error("version mismatch"));
    open({});
    await userEvent.type(screen.getByLabelText("Notice"), "Down for repair");
    await userEvent.click(save());
    expect(await screen.findByText(/version mismatch/)).toBeInTheDocument();
  });
});
