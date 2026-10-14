import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@/test/utils";
import userEvent from "@testing-library/user-event";
import { create } from "@bufbuild/protobuf";

import {
  LifecycleExpirationSchema,
  LifecycleRuleSchema,
  LifecycleTransitionSchema,
} from "@/gen/paladin/admin/v1/types_pb";
import type { Bucket, LifecycleRule } from "@/gen/paladin/admin/v1/types_pb";

const h = vi.hoisted(() => ({
  setRules: vi.fn(),
  setBucket: vi.fn(),
  notify: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: { setLifecycleRules: h.setRules },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));
vi.mock("@/hooks/useCELValidation", () => ({
  useCELValidation: () => ({ status: "valid" }),
}));
vi.mock("../../../../tenant-context", () => ({
  useTenantChangesBlocked: () => null,
}));

let bucket: Bucket;
vi.mock("../bucket-context", () => ({
  useBucket: () => ({ bucket, setBucket: h.setBucket, refetch: vi.fn() }),
}));

import BucketLifecyclePage from "./page";

const DAY_SECONDS = 86_400n;

const expiration = (id: string): LifecycleRule =>
  create(LifecycleRuleSchema, {
    id,
    enabled: true,
    match: "size_bytes > 0",
    action: {
      case: "expiration",
      value: create(LifecycleExpirationSchema, {
        after: { seconds: DAY_SECONDS },
      }),
    },
  });

const transition = (id: string): LifecycleRule =>
  create(LifecycleRuleSchema, {
    id,
    enabled: true,
    match: "size_bytes > 0",
    action: {
      case: "transition",
      value: create(LifecycleTransitionSchema, {
        after: { seconds: DAY_SECONDS },
        storageClass: "GLACIER",
      }),
    },
  });

const withRules = (rules: LifecycleRule[]): Bucket =>
  ({
    $typeName: "paladin.admin.v1.Bucket",
    name: "storageBackends/b1/buckets/bkt",
    resourceVersion: "3",
    lifecycleRules: rules,
  }) as unknown as Bucket;

describe("BucketLifecyclePage", () => {
  beforeEach(() => {
    h.setRules.mockReset();
    h.setBucket.mockReset();
    h.notify.mockReset();
    h.setRules.mockResolvedValue(withRules([]));
  });

  // The server refuses transitions, so the editor offers expiration only.
  it("creates an expiration rule and offers no transition", async () => {
    bucket = withRules([]);
    render(<BucketLifecyclePage />);
    await userEvent.click(
      screen.getAllByRole("button", { name: /add rule/i })[0],
    );

    expect(screen.queryByText(/storage class/i)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /transition/i }),
    ).not.toBeInTheDocument();

    await userEvent.type(screen.getByLabelText(/rule id/i), "expire-tmp");
    await userEvent.type(screen.getByLabelText(/match/i), "size_bytes > 0");
    await userEvent.click(screen.getByRole("button", { name: /create rule/i }));

    await waitFor(() => expect(h.setRules).toHaveBeenCalledTimes(1));
    const [rule] = h.setRules.mock.calls[0][0].rules;
    expect(rule.id).toBe("expire-tmp");
    expect(rule.action.case).toBe("expiration");
  });

  it("marks a stored transition as not applied and only lets it be deleted", async () => {
    bucket = withRules([expiration("keep"), transition("archive")]);
    render(<BucketLifecyclePage />);

    expect(screen.getByRole("alert")).toHaveTextContent(/never applied/);
    expect(screen.getByText("not applied")).toBeInTheDocument();
    expect(screen.getByText(/GLACIER .*not applied/)).toBeInTheDocument();

    await userEvent.click(
      screen.getByRole("button", { name: "Actions for rule archive" }),
    );
    expect(screen.getByRole("menuitem", { name: /edit/i })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    expect(screen.getByRole("menuitem", { name: /disable/i })).toHaveAttribute(
      "aria-disabled",
      "true",
    );

    await userEvent.click(screen.getByRole("menuitem", { name: /delete/i }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("archive");
    expect(within(confirm).getByRole("note")).toHaveTextContent(/stay expired/);
    expect(h.setRules).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: /delete rule/i }));
    await waitFor(() => expect(h.setRules).toHaveBeenCalledTimes(1));
    const sent = h.setRules.mock.calls[0][0].rules as LifecycleRule[];
    expect(sent.map((r) => r.id)).toEqual(["keep"]);
  });

  it("shows no warning for a bucket of expirations", () => {
    bucket = withRules([expiration("keep")]);
    render(<BucketLifecyclePage />);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
