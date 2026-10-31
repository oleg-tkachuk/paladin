import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConnectError, Code } from "@connectrpc/connect";
import userEvent from "@testing-library/user-event";

import { render, screen, within } from "@/test/utils";

const ACME = "11111111-1111-1111-1111-111111111111";
const POOL = "22222222-2222-2222-2222-222222222222";

const h = vi.hoisted(() => ({
  bucketsError: null as Error | null,
  listCollections: vi.fn(),
}));

vi.mock("@/lib/connect/client", () => ({
  backendClient: {
    listBackends: vi.fn(async () => ({
      backends: [
        {
          backendId: "be",
          enabled: false,
          readOnly: false,
          maintenance: false,
          healthStatus: "ok",
          healthMessage: "",
        },
        {
          backendId: "spare",
          enabled: true,
          readOnly: false,
          maintenance: false,
          healthStatus: "ok",
          healthMessage: "",
        },
      ],
      page: { nextPageToken: "" },
    })),
  },
  bucketClient: {
    listBuckets: vi.fn(async () => {
      if (h.bucketsError) throw h.bucketsError;
      return {
        buckets: [
          {
            backendId: "be",
            bucketId: "pool",
            ownerTenantId: POOL,
            grantedTenantIds: [ACME],
            provisionState: "failed",
          },
        ],
        page: { nextPageToken: "" },
      };
    }),
  },
  collectionClient: { listCollections: h.listCollections },
  tenantClient: {
    listTenants: vi.fn(async () => ({
      tenants: [
        { tenantId: ACME, slug: "acme", displayName: "Acme" },
        { tenantId: POOL, slug: "platform", displayName: "Platform" },
      ],
      page: { nextPageToken: "" },
    })),
  },
}));

// The header reads the route for its trail; the drawing is what is tested.
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));

import PlatformTopologyPage from "./page";

beforeEach(() => {
  h.bucketsError = null;
  h.listCollections.mockReset().mockResolvedValue({
    collections: [{ tenantId: ACME, collection: "docs", displayName: "" }],
    page: { nextPageToken: "" },
  });
});

describe("PlatformTopologyPage", () => {
  it("draws backends → buckets → tenants, each a link to its page", async () => {
    render(<PlatformTopologyPage />);
    const drawing = await screen.findByRole("group", {
      name: "Platform topology",
    });
    expect(
      within(drawing)
        .getAllByRole("link")
        .map((l) => l.getAttribute("href")),
    ).toEqual([
      "/storage-backends/be",
      "/storage-backends/spare",
      "/storage-backends/be/buckets/pool",
      "/tenants/acme",
      "/tenants/platform",
    ]);
  });

  // Tones come from backendView and the bucket's provisioning, the words
  // the storage-backends and bucket pages use.
  it("tones a backend by its lifecycle and a bucket by its provisioning", async () => {
    render(<PlatformTopologyPage />);
    expect(
      await screen.findByRole("img", { name: "Disabled" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("img", { name: "Provisioning failed" }),
    ).toBeInTheDocument();
  });

  it("draws a backend that holds no bucket apart", async () => {
    render(<PlatformTopologyPage />);
    const spare = await screen.findByRole("link", { name: /^spare/ });
    expect(spare.className).toContain("border-dashed");
  });

  it("says the buckets failed rather than drawing backends with none", async () => {
    h.bucketsError = new ConnectError("upstream", Code.Unavailable);
    render(<PlatformTopologyPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Buckets could not be loaded/,
    );
  });

  // A tenant opens down to its collections from its menu, reading that
  // tenant's alone, and closes again.
  it("shows a tenant's collections on demand, read for that tenant alone", async () => {
    render(<PlatformTopologyPage />);
    await screen.findByRole("group", { name: "Platform topology" });
    expect(h.listCollections).not.toHaveBeenCalled();

    await userEvent.click(
      screen.getByRole("button", { name: "More for Acme" }),
    );
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Show collections" }),
    );
    const docs = await screen.findByRole("link", { name: /^docs/ });
    expect(docs.getAttribute("href")).toBe("/tenants/acme/collections/docs");
    expect(h.listCollections).toHaveBeenCalledTimes(1);
    expect(h.listCollections.mock.calls[0][0]).toMatchObject({
      parent: `tenants/${ACME}`,
    });

    await userEvent.click(
      screen.getByRole("button", { name: "More for Acme" }),
    );
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Hide collections" }),
    );
    expect(screen.queryByRole("link", { name: /^docs/ })).toBeNull();
  });
});
