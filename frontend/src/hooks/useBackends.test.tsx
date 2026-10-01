import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";

const h = vi.hoisted(() => ({
  listBackends: vi.fn(),
  updateBackend: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  backendClient: {
    listBackends: h.listBackends,
    updateBackend: h.updateBackend,
  },
}));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
  useRefreshSignal: () => 0,
}));

import { useBackends } from "./useBackends";
import { EventTarget, SseType } from "@/gen/paladin/admin/v1/types_pb";

function page(ids: string[], nextPageToken = "") {
  return {
    backends: ids.map((backendId) => ({ backendId })),
    page: { nextPageToken },
  };
}

describe("useBackends pagination", () => {
  beforeEach(() => {
    h.listBackends.mockReset();
  });

  // A backend past the first page was invisible and unselectable everywhere it
  // is offered — the scope picker, the collection and bucket dialogs, the
  // tables — with nothing on screen saying the list was cut. Same defect the
  // bucket picker had; there are simply fewer backends, so it had not bitten.
  it("follows nextPageToken so backends past the first page are reachable", async () => {
    h.listBackends
      .mockResolvedValueOnce(page(["a", "b"], "tok-2"))
      .mockResolvedValueOnce(page(["z"]));

    const { result } = renderHook(() => useBackends(false));
    await act(async () => {
      await result.current.fetchBackends();
    });

    expect(h.listBackends).toHaveBeenCalledTimes(2);
    expect(h.listBackends.mock.calls[1][0].page.pageToken).toBe("tok-2");
    expect(result.current.backends.map((b) => b.backendId)).toEqual([
      "a",
      "b",
      "z",
    ]);
  });

  it("stops at the page ceiling instead of following a token forever", async () => {
    h.listBackends.mockResolvedValue(page(["x"], "always-more"));

    const { result } = renderHook(() => useBackends(false));
    await act(async () => {
      await result.current.fetchBackends();
    });

    expect(h.listBackends).toHaveBeenCalledTimes(20);
  });
});

// The advanced fields (sse / events / cedar_policy) are mask-gated as whole
// GROUPS on the server: naming "events" writes enabled, target, queue_url and
// poll_interval from the message in one statement. So the mask has to follow
// what the caller actually supplied — a fixed mask would let an unrelated edit
// rewrite a group from whatever the form happened to hold, and blank a KMS key
// id or an SQS queue URL nobody touched.
describe("useBackends update mask", () => {
  beforeEach(() => {
    h.updateBackend.mockReset();
    h.updateBackend.mockResolvedValue({ backendId: "primary" });
  });

  const flat = {
    displayName: "Primary",
    endpoint: "http://minio:9000",
    publicEndpoint: "http://localhost:9000",
    region: "us-east-1",
    forcePathStyle: true,
  };

  async function update(
    input: Parameters<ReturnType<typeof useBackends>["updateBackend"]>[2],
  ) {
    const { result } = renderHook(() => useBackends(false));
    await act(async () => {
      await result.current.updateBackend("primary", "7", input);
    });
    return h.updateBackend.mock.calls[0][0];
  }

  it("sends only the flat metadata paths when no advanced field is supplied", async () => {
    const req = await update(flat);
    expect(req.updateMask.paths).toEqual([
      "display_name",
      "endpoint",
      "public_endpoint",
      "region",
      "force_path_style",
    ]);
    // Not merely absent from the mask — absent from the message too, so a
    // server that ever widened its mask handling still could not read a value
    // this caller never sent.
    expect(req.backend.sse).toBeUndefined();
    expect(req.backend.events).toBeUndefined();
    expect(req.backend.cedarPolicy).toBe("");
  });

  it("adds the sse path and sub-message when sse is supplied", async () => {
    const req = await update({
      ...flat,
      sse: { type: SseType.KMS, keyId: "arn:aws:kms:key/abc" },
    });
    expect(req.updateMask.paths).toContain("sse");
    expect(req.updateMask.paths).not.toContain("events");
    expect(req.backend.sse.type).toBe(SseType.KMS);
    expect(req.backend.sse.keyId).toBe("arn:aws:kms:key/abc");
  });

  it("carries sub-second poll intervals through the Duration split", async () => {
    // 1500ms is the case a naive seconds conversion loses: truncating gives 1s,
    // and rounding gives 2s. Neither is the interval the operator typed.
    const req = await update({
      ...flat,
      events: {
        enabled: true,
        target: EventTarget.SQS,
        queueUrl: "https://sqs.example/q",
        pollIntervalMs: 1500,
      },
    });
    expect(req.updateMask.paths).toContain("events");
    expect(req.backend.events.enabled).toBe(true);
    expect(req.backend.events.target).toBe(EventTarget.SQS);
    expect(req.backend.events.queueUrl).toBe("https://sqs.example/q");
    expect(Number(req.backend.events.pollInterval.seconds)).toBe(1);
    expect(req.backend.events.pollInterval.nanos).toBe(500_000_000);
  });

  it("adds the cedar_policy path when a policy is supplied", async () => {
    const req = await update({
      ...flat,
      cedarPolicy: "forbid(principal,action,resource);",
    });
    expect(req.updateMask.paths).toContain("cedar_policy");
    expect(req.backend.cedarPolicy).toBe("forbid(principal,action,resource);");
  });

  // An empty string is a real value — "clear the policy" — and must not be
  // confused with "leave it alone". Only `undefined` means the latter.
  it("treats an empty cedar policy as a write, not an omission", async () => {
    const req = await update({ ...flat, cedarPolicy: "" });
    expect(req.updateMask.paths).toContain("cedar_policy");
  });
});
