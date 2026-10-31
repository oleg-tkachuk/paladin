import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { ConnectError, Code } from "@connectrpc/connect";

import { render, screen, within } from "@/test/utils";

const TENANT_ID = "11111111-1111-1111-1111-111111111111";
const POOL = "22222222-2222-2222-2222-222222222222";

const h = vi.hoisted(() => ({
  collections: [] as Array<Record<string, unknown>>,
  collectionsError: null as Error | null,
  backendsError: null as Error | null,
  capabilitiesError: null as Error | null,
}));

vi.mock("@/lib/connect/client", () => ({
  tenantClient: {
    getTenant: vi.fn(async () => ({
      tenantId: TENANT_ID,
      slug: "acme",
      displayName: "Acme",
      storageLayout: "dedicated",
      defaultBucket: "storageBackends/be/buckets/home",
    })),
  },
  collectionClient: {
    listCollections: vi.fn(async () => {
      if (h.collectionsError) throw h.collectionsError;
      return { collections: h.collections, page: { nextPageToken: "" } };
    }),
  },
  bucketClient: {
    listAccessibleBuckets: vi.fn(async () => ({
      buckets: [
        {
          backendId: "be",
          bucketId: "home",
          ownerTenantId: TENANT_ID,
          grantedTenantIds: [],
          provisionState: "ready",
        },
        {
          backendId: "be",
          bucketId: "common",
          ownerTenantId: POOL,
          grantedTenantIds: [TENANT_ID],
          provisionState: "pending",
        },
        {
          backendId: "be",
          bucketId: "spare",
          ownerTenantId: TENANT_ID,
          grantedTenantIds: [],
          provisionState: "ready",
        },
      ],
      page: { nextPageToken: "" },
    })),
  },
  capabilityClient: {
    list: vi.fn(async () => {
      if (h.capabilitiesError) throw h.capabilitiesError;
      return {
        capabilities: [
          { subject: { kind: 2, tenantId: TENANT_ID, subject: "mcp-relay" } },
        ],
        nextPageToken: "",
      };
    }),
  },
  backendClient: {
    listBackends: vi.fn(async () => {
      if (h.backendsError) throw h.backendsError;
      return {
        backends: [
          {
            backendId: "be",
            displayName: "Primary",
            enabled: true,
            readOnly: false,
            maintenance: false,
            healthStatus: "error",
            healthMessage: "refused",
          },
        ],
        page: { nextPageToken: "" },
      };
    }),
  },
}));
vi.mock("../tenant-context", () => ({
  useTenant: () => ({ tenantId: TENANT_ID, slug: "acme", displayName: "Acme" }),
}));

import TenantTopologyPage from "./page";

const collection = (name: string, bucket: string) => ({
  tenantId: TENANT_ID,
  collection: name,
  displayName: "",
  bucket: `storageBackends/be/buckets/${bucket}`,
});

beforeEach(() => {
  h.collections = [collection("docs", "home"), collection("media", "common")];
  h.collectionsError = null;
  h.backendsError = null;
  h.capabilitiesError = null;
});

/** The drawing's node links, in the order focus moves through them. */
async function nodeLinks() {
  const drawing = await screen.findByRole("group", {
    name: "Topology of acme",
  });
  return within(drawing).getAllByRole("link");
}

describe("TenantTopologyPage", () => {
  it("draws tenant → collections → buckets → backend and its access, each a link to its page", async () => {
    render(<TenantTopologyPage />);
    const links = await nodeLinks();
    // Column by column; each bucket level with the collection bound to it,
    // so no two edges cross.
    expect(links.map((l) => l.getAttribute("href"))).toEqual([
      "/tenants/acme",
      "/tenants/acme/collections/docs",
      "/tenants/acme/collections/media",
      // The tenant's access, a branch beside its collections.
      "/tenants/acme/capabilities",
      "/storage-backends/be/buckets/home",
      "/storage-backends/be/buckets/common",
      "/storage-backends/be/buckets/spare",
      "/storage-backends/be",
    ]);
  });

  it("states each node's state in words as well as a glyph", async () => {
    render(<TenantTopologyPage />);
    await nodeLinks();
    expect(
      screen.getByRole("img", { name: "Provisioning" }),
    ).toBeInTheDocument();
    // The backend's failed probe outranks its being enabled.
    expect(
      screen.getByRole("img", { name: "Probe failed" }),
    ).toBeInTheDocument();
  });

  it("marks the default bucket and draws an unused one apart", async () => {
    render(<TenantTopologyPage />);
    const links = await nodeLinks();
    const home = links.find((l) => l.getAttribute("href")?.endsWith("/home"))!;
    expect(home).toHaveTextContent("Default bucket");
    const spare = links.find((l) =>
      l.getAttribute("href")?.endsWith("/spare"),
    )!;
    expect(spare.className).toContain("border-dashed");
    expect(home.className).not.toContain("border-dashed");
    expect(screen.getByRole("list", { name: "Legend" })).toHaveTextContent(
      "Used by nothing",
    );
  });

  it("dims what a filter does not match, and offers to clear one that matches nothing", async () => {
    const user = userEvent.setup();
    render(<TenantTopologyPage />);
    await nodeLinks();
    const box = screen.getByRole("searchbox", { name: "Filter the topology" });
    await user.type(box, "media");
    const media = screen.getByRole("link", { name: /media/ });
    const docs = screen.getByRole("link", { name: /docs/ });
    expect(media.className).not.toContain("opacity-40");
    expect(docs.className).toContain("opacity-40");

    await user.clear(box);
    await user.type(box, "nothing-has-this");
    await user.click(screen.getByRole("button", { name: "Clear filter" }));
    expect(box).toHaveValue("");
  });

  it("zooms in and out by steps, within bounds", async () => {
    const user = userEvent.setup();
    render(<TenantTopologyPage />);
    await nodeLinks();
    expect(screen.getByText("100%")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Zoom in" }));
    expect(screen.getByText("110%")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Zoom out" }));
    await user.click(screen.getByRole("button", { name: "Zoom out" }));
    expect(screen.getByText("90%")).toBeInTheDocument();
  });

  it("folds many collections into one node that opens on demand", async () => {
    h.collections = Array.from({ length: 7 }, (_, i) =>
      collection(`c${i}`, "home"),
    );
    const user = userEvent.setup();
    render(<TenantTopologyPage />);
    const group = await screen.findByRole("button", {
      name: "Show the 7 collections one by one",
    });
    expect(screen.queryByRole("link", { name: /^c0/ })).not.toBeInTheDocument();
    await user.click(group);
    expect(screen.getByRole("link", { name: /^c0/ })).toBeInTheDocument();

    // Turning grouping off and on folds them again.
    const toggle = screen.getByRole("button", { name: /Group siblings/ });
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    await user.click(toggle);
    expect(
      screen.getByRole("button", { name: "Show the 7 collections one by one" }),
    ).toBeInTheDocument();
  });

  it("says the collections failed rather than drawing a tenant with none", async () => {
    h.collectionsError = new ConnectError("upstream", Code.Unavailable);
    render(<TenantTopologyPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Collections could not be loaded/,
    );
    expect(
      screen.queryByRole("group", { name: "Topology of acme" }),
    ).not.toBeInTheDocument();
  });

  // A tenant's own admin may not list backends.
  it("draws without backend states when the backends cannot be read", async () => {
    h.backendsError = new ConnectError("denied", Code.PermissionDenied);
    render(<TenantTopologyPage />);
    const links = await nodeLinks();
    expect(links.at(-1)).toHaveAttribute("href", "/storage-backends/be");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Storage backends could not be loaded/,
    );
    expect(
      screen.queryByRole("img", { name: "Probe failed" }),
    ).not.toBeInTheDocument();
  });

  // The tenant's access is a branch off it: a principal holding live
  // capabilities leads to them, and a failed read draws the rest without it.
  it("draws each principal holding live capabilities off the tenant", async () => {
    render(<TenantTopologyPage />);
    const links = await nodeLinks();
    const principal = links.find((l) => l.textContent?.includes("mcp-relay"));
    expect(principal?.getAttribute("href")).toBe("/tenants/acme/capabilities");
    expect(principal?.textContent).toContain("1 live capability");
  });

  it("draws without principals when the capabilities cannot be read", async () => {
    h.capabilitiesError = new ConnectError("no", Code.PermissionDenied);
    render(<TenantTopologyPage />);
    const links = await nodeLinks();
    expect(links.some((l) => l.textContent?.includes("mcp-relay"))).toBe(false);
    expect(await screen.findByText(/Capabilities/)).toBeInTheDocument();
  });
});
