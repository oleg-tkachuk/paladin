import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  listBackends: vi.fn(),
  updateBackend: vi.fn(),
  setBackendEnabled: vi.fn(),
  setBackendReadOnly: vi.fn(),
  setBackendMaintenance: vi.fn(),
  deleteBackend: vi.fn(),
  createBackend: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({ backendClient: h }));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
}));

import { useBackends } from "./useBackends";
import { MAX_LIST_PAGES } from "./useBuckets";
import {
  EventTarget,
  SseType,
  type StorageBackend,
} from "@/gen/paladin/admin/v1/types_pb";
import { backendsQuery } from "@/lib/queries/backends";
import { policyVersionsKey } from "@/lib/queries/policyVersions";

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}
function wrap(qc: QueryClient = client()) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function page(ids: string[], nextPageToken = "") {
  return {
    backends: ids.map((backendId) => ({ backendId })),
    page: { nextPageToken },
  };
}

beforeEach(() => {
  for (const fn of Object.values(h)) fn.mockReset();
});

describe("useBackends pagination", () => {
  // A backend past the first page was invisible and unselectable everywhere it
  // is offered — the scope picker, the collection and bucket dialogs, the
  // tables — with nothing on screen saying the list was cut. Same defect the
  // bucket picker had; there are simply fewer backends, so it had not bitten.
  it("follows nextPageToken so backends past the first page are reachable", async () => {
    h.listBackends
      .mockResolvedValueOnce(page(["a", "b"], "tok-2"))
      .mockResolvedValueOnce(page(["z"]));

    const { result } = renderHook(() => useBackends(), { wrapper: wrap() });
    await waitFor(() => expect(result.current.backends).toHaveLength(3));

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

    const { result } = renderHook(() => useBackends(), { wrapper: wrap() });
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(h.listBackends).toHaveBeenCalledTimes(MAX_LIST_PAGES);
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
    const { result } = renderHook(() => useBackends({ enabled: false }), {
      wrapper: wrap(),
    });
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

describe("useBackends reads", () => {
  it("asks for the filter it is given", async () => {
    h.listBackends.mockResolvedValue(page([]));
    renderHook(() => useBackends({ filter: "enabled == true" }), {
      wrapper: wrap(),
    });
    await waitFor(() => expect(h.listBackends).toHaveBeenCalled());
    expect(h.listBackends.mock.calls[0][0].filter).toBe("enabled == true");
  });

  it("reads nothing while held, as a component that only changes them", () => {
    renderHook(() => useBackends({ enabled: false }), { wrapper: wrap() });
    expect(h.listBackends).not.toHaveBeenCalled();
  });
});

describe("useBackends changes", () => {
  const backend = (backendId: string, extra: Partial<StorageBackend> = {}) =>
    ({
      backendId,
      displayName: backendId,
      enabled: true,
      readOnly: false,
      maintenance: false,
      ...extra,
    }) as StorageBackend;

  /** Two backends cached, and a re-read that never answers. */
  function seeded() {
    const qc = client();
    h.listBackends.mockImplementation(() => new Promise(() => {}));
    qc.setQueryData(backendsQuery().queryKey, [backend("a"), backend("b")]);
    const { result } = renderHook(() => useBackends({ enabled: false }), {
      wrapper: wrap(qc),
    });
    const cached = () =>
      qc.getQueryData<StorageBackend[]>(backendsQuery().queryKey);
    return { qc, result, cached };
  }

  /** A server call that refuses when the test says so. */
  function refusing(fn: ReturnType<typeof vi.fn>) {
    let refuse!: (e: Error) => void;
    fn.mockImplementation(() => new Promise((_, rej) => (refuse = rej)));
    return (e: Error) => refuse(e);
  }

  it.each([
    ["setBackendEnabled", "enabled", false],
    ["setBackendReadOnly", "readOnly", true],
    ["setBackendMaintenance", "maintenance", true],
  ] as const)(
    "%s shows at once and is put back when refused",
    async (fn, field, value) => {
      const refuse = refusing(h[fn]);
      const { result, cached } = seeded();
      let done!: Promise<unknown>;
      act(() => {
        done = result.current[fn]("a", value, "v1").catch((e) => e);
      });
      await waitFor(() => expect(cached()?.[0][field]).toBe(value));
      expect(cached()?.[1][field]).toBe(!value);
      await act(async () => {
        refuse(new Error("default backend"));
        await done;
      });
      expect(cached()?.[0][field]).toBe(!value);
    },
  );

  it("takes a deleted backend out at once, and back when refused", async () => {
    const refuse = refusing(h.deleteBackend);
    const { result, cached } = seeded();
    let done!: Promise<unknown>;
    act(() => {
      done = result.current.deleteBackend("a", "v1").catch((e) => e);
    });
    await waitFor(() =>
      expect(cached()?.map((b) => b.backendId)).toEqual(["b"]),
    );
    await act(async () => {
      refuse(new Error("buckets still reference it"));
      await done;
    });
    expect(cached()?.map((b) => b.backendId)).toEqual(["a", "b"]);
  });

  it("shows an edit's plain fields at once", async () => {
    refusing(h.updateBackend);
    const { result, cached } = seeded();
    act(() => {
      void result.current
        .updateBackend("a", "v1", {
          displayName: "Primary",
          endpoint: "http://minio:9000",
          publicEndpoint: "",
          region: "eu",
          forcePathStyle: true,
        })
        .catch(() => {});
    });
    await waitFor(() => expect(cached()?.[0].displayName).toBe("Primary"));
    expect(cached()?.[0].region).toBe("eu");
  });

  // A policy written is the backend layer's newest version; an edit that
  // leaves the policy alone does not touch its history.
  it.each([
    ["a policy edit reads the layer's history again", "permit", true],
    ["an edit without the policy does not", undefined, false],
  ] as const)("%s", async (_, cedarPolicy, reread) => {
    h.updateBackend.mockResolvedValue(backend("a"));
    const { qc, result } = seeded();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    await act(() =>
      result.current.updateBackend("a", "v1", {
        displayName: "A",
        endpoint: "http://minio:9000",
        publicEndpoint: "",
        region: "",
        forcePathStyle: false,
        ...(cedarPolicy !== undefined && { cedarPolicy }),
      }),
    );
    const historyKey = { queryKey: policyVersionsKey("storageBackends/a") };
    if (reread) expect(invalidate).toHaveBeenCalledWith(historyKey);
    else expect(invalidate).not.toHaveBeenCalledWith(historyKey);
  });

  it("throws the refusal on to the caller and reads every list again", async () => {
    h.setBackendEnabled.mockRejectedValue(new Error("stale"));
    const { qc, result } = seeded();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    await expect(
      act(() => result.current.setBackendEnabled("a", false, "v1")),
    ).rejects.toThrow("stale");
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["backends"] });
  });

  // A new backend is the server's to describe: nothing is predicted, and
  // every list reads again to show it.
  it("reads every list again after a create", async () => {
    h.createBackend.mockResolvedValue(backend("c"));
    const { qc, result } = seeded();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    await act(() =>
      result.current.createBackend({
        backendId: "c",
        displayName: "c",
        kind: 0,
        endpoint: "http://c",
        publicEndpoint: "",
        region: "",
        forcePathStyle: false,
        credentialsSecretRef: "",
      } as never),
    );
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["backends"] });
  });
});
