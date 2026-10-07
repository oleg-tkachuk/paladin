import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({ resolve: vi.fn() }));

vi.mock("next/navigation", () => ({
  notFound: vi.fn(),
  useParams: () => ({ id: "acme" }),
  usePathname: () => "/tenants/acme",
}));
vi.mock("@/lib/resources/tenant-resolve", () => ({
  useTenantResolve: h.resolve,
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({ title }: { title: string }) => <h1>{title}</h1>,
}));
vi.mock("@/components/layout/TenantTabs", () => ({
  TenantTabs: () => null,
}));

import TenantLayout from "./layout";

function resolveTo(trashed: boolean) {
  h.resolve.mockReturnValue({
    tenant: {
      tenantId: "t-1",
      slug: "acme",
      displayName: "Acme",
      storageLayout: "shared",
      trashed,
    },
    loading: false,
    error: null,
    errorIsNotFound: false,
    retry: vi.fn(),
  });
}

beforeEach(() => {
  h.resolve.mockReset();
});

describe("TenantLayout trash banner", () => {
  it("says a trashed tenant is frozen and links to the trash", () => {
    resolveTo(true);
    render(<TenantLayout>page</TenantLayout>);
    expect(screen.getByRole("status")).toHaveTextContent(
      "This tenant is in the trash.",
    );
    expect(
      screen.getByRole("link", { name: "Restore or purge it from the Trash" }),
    ).toHaveAttribute("href", "/trash");
  });

  it("shows no banner for a live tenant", () => {
    resolveTo(false);
    render(<TenantLayout>page</TenantLayout>);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});
