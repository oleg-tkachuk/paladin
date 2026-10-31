import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@/test/utils";

const h = vi.hoisted(() => ({ list: vi.fn(), tenants: vi.fn() }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/capabilities",
  useSearchParams: () => new URLSearchParams(""),
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: { list: h.list },
  tenantClient: { listTenants: h.tenants },
}));

import {
  CapabilityDisposition,
  PrincipalKind,
} from "@/gen/paladin/admin/v1/capability_service_pb";

import PlatformCapabilitiesPage from "./page";

const ACME = "0199-acme";
const CAP = {
  id: "cap-1",
  clientId: "claude-agent",
  subject: { kind: PrincipalKind.AGENT, tenantId: ACME, subject: "mcp-relay" },
  issuedAt: { seconds: 1_700_000_000n },
  expiresAt: { seconds: 1_700_000_600n },
  disposition: CapabilityDisposition.ACTIVE,
};

describe("/capabilities", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/capabilities");
    h.list.mockReset();
    h.tenants.mockReset().mockResolvedValue({
      tenants: [{ tenantId: ACME, slug: "acme", displayName: "Acme" }],
      page: { nextPageToken: "" },
    });
  });

  it("lists every tenant's, each under its tenant, the active ones first asked for", async () => {
    h.list.mockResolvedValue({ capabilities: [CAP], nextPageToken: "" });
    render(<PlatformCapabilitiesPage />);
    const row = await screen.findByRole("row", { name: /mcp-relay/ });
    expect(
      within(row).getByRole("link", { name: "Acme" }).getAttribute("href"),
    ).toBe("/tenants/acme");
    expect(
      within(row).getByRole("link", { name: "mcp-relay" }).getAttribute("href"),
    ).toContain("/tenants/acme/capabilities");
    expect(h.list).toHaveBeenCalledWith(
      expect.objectContaining({
        tenantId: "",
        dispositions: [CapabilityDisposition.ACTIVE],
      }),
      expect.anything(),
    );
  });

  it("names a tenant it cannot find by its id", async () => {
    h.tenants.mockResolvedValue({ tenants: [], page: { nextPageToken: "" } });
    h.list.mockResolvedValue({ capabilities: [CAP], nextPageToken: "" });
    render(<PlatformCapabilitiesPage />);
    const row = await screen.findByRole("row", { name: /mcp-relay/ });
    expect(within(row).getByRole("link", { name: ACME })).toBeInTheDocument();
  });

  it("says what the list is for when there is none", async () => {
    h.list.mockResolvedValue({ capabilities: [], nextPageToken: "" });
    render(<PlatformCapabilitiesPage />);
    expect(await screen.findByText("No capabilities yet.")).toBeInTheDocument();
  });

  // A tenant admin is refused every tenant's; the page says why rather than
  // showing an empty list.
  it("says why the list could not be read", async () => {
    h.list.mockRejectedValue(
      new Error("listing every tenant's capabilities requires platform.admin"),
    );
    render(<PlatformCapabilitiesPage />);
    expect(
      await screen.findByText(/requires platform.admin/),
    ).toBeInTheDocument();
  });
});
