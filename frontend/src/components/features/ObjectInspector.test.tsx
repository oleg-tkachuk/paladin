import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@/test/utils";
import userEvent from "@testing-library/user-event";

import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import {
  TenantProvider,
  TRASHED_TENANT_BLOCK,
} from "@/app/tenants/[id]/tenant-context";

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

// A public collection has no trash: the server refuses a soft delete there, so
// the drawer offers none and shows the address a consumer stores.
describe("ObjectInspector on a public object", () => {
  const URL = "https://s3.example.test/pub/acme-uuid/docs/k";

  it("shows the public URL and offers no trash", () => {
    h.object = { ...h.object, publicUrl: URL };
    renderInTenant();
    expect(screen.getByText(URL)).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Trash" }),
    ).not.toBeInTheDocument();
  });

  it("keeps the trash for a private object", () => {
    renderInTenant();
    expect(screen.queryByText("Public URL")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Trash" })).toBeInTheDocument();
  });
});

describe("ObjectInspector on a trashed tenant", () => {
  it("holds the trash and keeps the details open", () => {
    render(
      <TenantProvider value={{ ...TENANT, trashed: true }}>
        <ObjectInspector
          collection="report.pdf"
          parentCollection="docs"
          onClose={() => {}}
        />
      </TenantProvider>,
    );
    const trash = screen.getByRole("button", { name: "Trash" });
    expect(trash).toBeDisabled();
    expect(trash).toHaveAttribute("title", TRASHED_TENANT_BLOCK);
    expect(screen.getByRole("button", { name: "Full Details" })).toBeEnabled();
  });
});

// Trash and restore from the drawer are confirmed first, and the drawer only
// closes once the trash has gone through.
describe("ObjectInspector confirmations", () => {
  function renderWith(onClose = vi.fn()) {
    render(
      <TenantProvider value={TENANT}>
        <ObjectInspector
          collection="report.pdf"
          parentCollection="docs"
          onClose={onClose}
        />
      </TenantProvider>,
    );
    return onClose;
  }

  it("asks before trashing and closes only after the trash lands", async () => {
    const onClose = renderWith();
    await userEvent.click(screen.getByRole("button", { name: "Trash" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("report.pdf");
    expect(within(dialog).getByRole("note")).toHaveTextContent(/restorable/);
    expect(h.softDeleteObject).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();

    await userEvent.click(
      within(dialog).getByRole("button", { name: "Move to Trash" }),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce());
    expect(h.softDeleteObject).toHaveBeenCalledOnce();
  });

  it("keeps the drawer open when the trash is refused", async () => {
    h.softDeleteObject.mockRejectedValueOnce(new Error("locked"));
    const onClose = renderWith();
    await userEvent.click(screen.getByRole("button", { name: "Trash" }));
    await userEvent.click(
      await screen.findByRole("button", { name: "Move to Trash" }),
    );
    await waitFor(() => expect(h.softDeleteObject).toHaveBeenCalledOnce());
    expect(onClose).not.toHaveBeenCalled();
  });

  it("does nothing when the trash is cancelled", async () => {
    const onClose = renderWith();
    await userEvent.click(screen.getByRole("button", { name: "Trash" }));
    await userEvent.click(
      await screen.findByRole("button", { name: "Cancel" }),
    );
    expect(h.softDeleteObject).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("asks before restoring a trashed object", async () => {
    h.object = { ...h.object, state: ObjectState.DELETED };
    renderWith();
    await userEvent.click(
      screen.getByRole("button", { name: /Restore Object/ }),
    );
    const dialog = await screen.findByRole("alertdialog");
    expect(h.restoreObject).not.toHaveBeenCalled();
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Restore object" }),
    );
    await waitFor(() => expect(h.restoreObject).toHaveBeenCalledOnce());
  });
});
