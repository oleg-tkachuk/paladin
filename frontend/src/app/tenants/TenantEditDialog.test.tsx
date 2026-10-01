import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { Code, ConnectError } from "@connectrpc/connect";

const h = vi.hoisted(() => ({ showNotification: vi.fn() }));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { TenantEditDialog } from "./TenantEditDialog";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";

const TENANT = {
  tenantId: "t-1",
  slug: "acme",
  displayName: "Acme",
  resourceVersion: "3",
  labels: {},
} as unknown as Tenant;

function open(update = vi.fn()) {
  render(
    <TenantEditDialog
      editing={TENANT}
      onClose={vi.fn()}
      updateTenantMetadata={update}
    />,
  );
  return update;
}

describe("TenantEditDialog", () => {
  beforeEach(() => {
    h.showNotification.mockReset();
  });

  it("holds Save until something changed", () => {
    open();
    expect(screen.getByText("No change to save.")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Display name"), {
      target: { value: "Acme Ltd" },
    });
    expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled();
  });

  // The server's reason — a duplicate name, a version conflict — used to be
  // replaced by "Failed to update tenant display name."
  it("shows why the update failed", async () => {
    open(
      vi
        .fn()
        .mockRejectedValue(
          new ConnectError("display name already used", Code.AlreadyExists),
        ),
    );
    fireEvent.change(screen.getByLabelText("Display name"), {
      target: { value: "Other" },
    });
    fireEvent.submit(
      screen.getByRole("button", { name: "Save changes" }).closest("form")!,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "display name already used",
    );
  });
});
