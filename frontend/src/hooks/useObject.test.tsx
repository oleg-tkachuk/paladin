import { afterEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import { PRESIGN_EXPIRY_SKEW_MS } from "@/lib/upload/retry";

const h = vi.hoisted(() => ({
  lookupObject: vi.fn(),
  presignDownload: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  objectClient: { lookupObject: h.lookupObject },
  presignClient: { presignDownload: h.presignDownload },
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({ user: { tenantId: "t1" } }),
}));
vi.mock("@/context/RefreshContext", () => ({
  useRefreshSignal: () => 0,
  useBumpRefresh: () => vi.fn(),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

import { downloadUrlLifetime, useObject } from "./useObject";

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

afterEach(() => vi.clearAllMocks());

// The download link used to be presigned with no Content-Disposition, so the
// store served the object inline from its own origin under the key's UUID
// name, and the query held the URL past its expiry.
describe("useObject", () => {
  it("presigns the download as an attachment named after the key", async () => {
    h.lookupObject.mockResolvedValue({
      name: "tenants/t1/collections/default/objects/o1",
      key: "docs/report.pdf",
      state: ObjectState.AVAILABLE,
    });
    h.presignDownload.mockResolvedValue({
      downloadUrl: { url: "https://s3/x", expiresAtRfc3339: "" },
    });
    renderHook(() => useObject("docs/report.pdf"), { wrapper });
    await waitFor(() => expect(h.presignDownload).toHaveBeenCalled());
    expect(h.presignDownload).toHaveBeenCalledWith(
      {
        name: "tenants/t1/collections/default/objects/o1",
        contentDisposition: "attachment; filename*=UTF-8''report.pdf",
      },
      expect.anything(),
    );
  });
});

describe("downloadUrlLifetime", () => {
  const now = Date.parse("2027-01-15T08:00:00Z");
  const object = {} as never;

  it("is how long the URL stays usable", () => {
    const left = 60_000;
    const expiresAtRfc3339 = new Date(
      now + PRESIGN_EXPIRY_SKEW_MS + left,
    ).toISOString();
    expect(
      downloadUrlLifetime(
        { object, downloadUrl: { expiresAtRfc3339 } as never },
        now,
      ),
    ).toBe(left);
  });

  it("is undefined without a URL", () => {
    expect(downloadUrlLifetime({ object, downloadUrl: null }, now)).toBe(
      undefined,
    );
    expect(downloadUrlLifetime(undefined, now)).toBeUndefined();
  });
});
