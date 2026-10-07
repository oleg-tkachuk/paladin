import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";
import userEvent from "@testing-library/user-event";

import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import { TenantProvider } from "@/app/tenants/[id]/tenant-context";

const h = vi.hoisted(() => ({
  object: null as Record<string, unknown> | null,
  softDeleteObject: vi.fn(() => Promise.resolve()),
  restoreObject: vi.fn(() => Promise.resolve()),
  push: vi.fn(),
}));

vi.mock("@/hooks/useObject", () => ({
  useObject: () => ({
    object: h.object,
    downloadUrl: undefined,
    loading: false,
    softDeleteObject: h.softDeleteObject,
    restoreObject: h.restoreObject,
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: h.push }),
}));
// The scope picker sits on the platform tenant, as it does for an operator
// browsing another tenant's pages.
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({
    collection: "",
    tenantId: "platform-uuid",
    tenant: { slug: "platform" },
  }),
}));

import { ObjectInspector } from "./ObjectInspector";

const TENANT = {
  tenantId: "acme-uuid",
  slug: "acme",
  displayName: "Acme",
  storageLayout: "shared",
  trashed: false,
};

function renderInTenant() {
  render(
    <TenantProvider value={TENANT}>
      <ObjectInspector
        collection="report.pdf"
        parentCollection="docs"
        onClose={() => {}}
      />
    </TenantProvider>,
  );
}

beforeEach(() => {
  h.object = {
    objectId: "obj-1",
    key: "report.pdf",
    collection: "docs",
    contentType: "application/pdf",
    sizeBytes: 10n,
    state: ObjectState.AVAILABLE,
    tags: {},
    publicUrl: "",
  };
  h.push.mockClear();
  h.softDeleteObject.mockClear();
  h.restoreObject.mockClear();
});

// Full Details took the tenant from the scope picker, so on another tenant's
// page it opened /tenants/platform/... and landed on a 404.
describe("ObjectInspector Full Details", () => {
  it("opens the object under the tenant whose page it is on", async () => {
    renderInTenant();
    await userEvent.click(screen.getByRole("button", { name: "Full Details" }));
    expect(h.push).toHaveBeenCalledWith(
      "/tenants/acme/collections/docs/objects/report.pdf",
    );
  });

  it("falls back to the scope picker outside a tenant page", async () => {
    render(
      <ObjectInspector
        collection="report.pdf"
        parentCollection="docs"
        onClose={() => {}}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Full Details" }));
    expect(h.push).toHaveBeenCalledWith(
      "/tenants/platform/collections/docs/objects/report.pdf",
    );
  });
});
