import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const h = vi.hoisted(() => ({
  backends: [{ backendId: "primary", displayName: "" }] as Array<
    Record<string, unknown>
  >,
  buckets: [] as Array<Record<string, unknown>>,
  backendsError: null as string | null,
  bucketsError: null as string | null,
  showNotification: vi.fn(),
}));
vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({
    backends: h.backends,
    error: h.backendsError,
    fetchBackends: vi.fn(),
  }),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({
    buckets: h.buckets,
    error: h.bucketsError,
    fetchBuckets: vi.fn(),
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { SLUG_ERROR, TenantCreateDialog } from "./TenantCreateDialog";

function open(createTenant = vi.fn()) {
  render(
    <TenantCreateDialog
      open
      onOpenChange={vi.fn()}
      createTenant={createTenant}
    />,
  );
  return createTenant;
}

const typeSlug = (v: string) =>
  fireEvent.change(screen.getByLabelText(/Slug/), { target: { value: v } });

describe("TenantCreateDialog", () => {
  beforeEach(() => {
    h.backends = [{ backendId: "primary", displayName: "" }];
    h.buckets = [];
    h.backendsError = null;
    h.bucketsError = null;
    h.showNotification.mockReset();
  });

  it("says what the held submit is waiting for", () => {
    open();
    expect(screen.getByText("Enter a slug to continue.")).toBeInTheDocument();
    typeSlug("acme");
    expect(screen.getByText("Pick a bucket to continue.")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Create tenant" }),
    ).toBeDisabled();
  });

  it("explains a bad slug at the field", () => {
    open();
    typeSlug("Bad_Slug");
    expect(screen.getByLabelText(/Slug/)).toHaveAccessibleDescription(
      SLUG_ERROR,
    );
  });

  it("points at the missing bucket beside the storage fields", () => {
    open();
    expect(
      screen.getByText(/has no bucket to bind to yet/),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Open Buckets/ })).toHaveAttribute(
      "href",
      "/buckets",
    );
  });

  // With one bucket on the backend there is nothing to choose.
  it("picks the only bucket and creates the tenant bound to it", async () => {
    h.buckets = [{ backendId: "primary", bucketId: "shared-1" }];
    const createTenant = open(
      vi.fn().mockResolvedValue({ slug: "acme", tenantId: "t" }),
    );
    typeSlug("acme");
    const submit = screen.getByRole("button", { name: "Create tenant" });
    expect(submit).toBeEnabled();
    fireEvent.submit(submit.closest("form")!);
    await waitFor(() => expect(createTenant).toHaveBeenCalledTimes(1));
    expect(createTenant.mock.calls[0][4]).toBe(
      "storageBackends/primary/buckets/shared-1",
    );
  });

  it("keeps a failed create in the dialog, with the reason", async () => {
    h.buckets = [{ backendId: "primary", bucketId: "shared-1" }];
    open(vi.fn().mockRejectedValue(new Error("slug already taken")));
    typeSlug("acme");
    fireEvent.submit(
      screen.getByRole("button", { name: "Create tenant" }).closest("form")!,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "slug already taken",
    );
  });

  // Each of these used to answer a failed read with advice: "none is
  // registered — open Storage Backends", "no bucket to bind to yet — open
  // Buckets". The read failed; nothing needs registering.
  it("says the backend list failed rather than sending the operator to register one", () => {
    h.backends = [];
    h.backendsError = "unavailable: upstream";
    open();
    typeSlug("acme");
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Backends could not be loaded/,
    );
    expect(
      screen.getByText("Backends could not be loaded."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/none is registered/)).not.toBeInTheDocument();
  });

  it("says the bucket list failed rather than that the backend has none", () => {
    h.bucketsError = "unavailable: upstream";
    open();
    typeSlug("acme");
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Buckets could not be loaded/,
    );
    expect(
      screen.getByText("Buckets could not be loaded."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/has no bucket to bind to/),
    ).not.toBeInTheDocument();
  });
});
