import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

import { ObjectLockMode } from "@/gen/paladin/admin/v1/types_pb";
import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";

const h = vi.hoisted(() => ({
  setLock: vi.fn(),
  setBucket: vi.fn(),
  notify: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: { setObjectLock: h.setLock },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));

// The page reads the bucket from context; feed it a mutable holder so each
// test can seed a different objectLock state.
let bucket: Bucket;
vi.mock("../bucket-context", () => ({
  useBucket: () => ({ bucket, setBucket: h.setBucket, refetch: vi.fn() }),
}));

import BucketObjectLockPage from "./page";

const baseBucket = (over: Partial<Bucket> = {}): Bucket =>
  ({
    $typeName: "paladin.admin.v1.Bucket",
    name: "storageBackends/b1/buckets/bkt",
    resourceVersion: "3",
    objectLock: undefined,
    ...over,
  }) as unknown as Bucket;

describe("BucketObjectLockPage", () => {
  beforeEach(() => {
    h.setLock.mockReset();
    h.setBucket.mockReset();
    h.notify.mockReset();
    h.setLock.mockResolvedValue(baseBucket());
  });

  it("Save is disabled until something changes", () => {
    bucket = baseBucket();
    render(<BucketObjectLockPage />);
    expect(screen.getByRole("button", { name: /save/i })).toBeDisabled();
  });

  it("enables lock with a governance default + retention days → seconds", async () => {
    bucket = baseBucket();
    render(<BucketObjectLockPage />);

    await userEvent.click(
      screen.getByRole("switch", { name: /enable object lock/i }),
    );
    await userEvent.type(screen.getByLabelText(/default retention/i), "30");
    await userEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(h.setLock).toHaveBeenCalledTimes(1));
    const arg = h.setLock.mock.calls[0][0];
    expect(arg.name).toBe("storageBackends/b1/buckets/bkt");
    expect(arg.resourceVersion).toBe("3");
    expect(arg.config.enabled).toBe(true);
    expect(arg.config.defaultMode).toBe(ObjectLockMode.GOVERNANCE);
    // 30 days → 30 * 86_400 = 2_592_000 s
    expect(arg.config.defaultRetention.seconds).toBe(2_592_000n);
    // On success the updated bucket is pushed back into context.
    expect(h.setBucket).toHaveBeenCalled();
  });

  it("disabling a locked bucket clears mode + retention", async () => {
    bucket = baseBucket({
      objectLock: {
        $typeName: "paladin.admin.v1.ObjectLockConfig",
        enabled: true,
        defaultMode: ObjectLockMode.COMPLIANCE,
        defaultRetention: {
          $typeName: "google.protobuf.Duration",
          seconds: 2_592_000n,
          nanos: 0,
        },
      },
    } as unknown as Partial<Bucket>);
    render(<BucketObjectLockPage />);

    await userEvent.click(
      screen.getByRole("switch", { name: /enable object lock/i }),
    );
    await userEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(h.setLock).toHaveBeenCalledTimes(1));
    const arg = h.setLock.mock.calls[0][0];
    expect(arg.config.enabled).toBe(false);
    expect(arg.config.defaultMode).toBe(ObjectLockMode.UNSPECIFIED);
    expect(arg.config.defaultRetention).toBeUndefined();
  });
});
