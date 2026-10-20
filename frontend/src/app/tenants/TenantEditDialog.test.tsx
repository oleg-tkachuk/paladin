import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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

  describe("notice", () => {
    const save = () =>
      fireEvent.submit(
        screen.getByRole("button", { name: "Save changes" }).closest("form")!,
      );

    // Written into the tenant's labels beside the ones it has, which go back
    // as they were.
    it("writes the notice into the labels and keeps the others", async () => {
      const update = vi.fn().mockResolvedValue(TENANT);
      render(
        <TenantEditDialog
          editing={{ ...TENANT, labels: { team: "storage" } } as Tenant}
          onClose={vi.fn()}
          updateTenantMetadata={update}
        />,
      );
      fireEvent.change(screen.getByLabelText("Notice"), {
        target: { value: "Moving to the new backend" },
      });
      fireEvent.change(screen.getByLabelText("Link"), {
        target: { value: "https://status.example.com" },
      });
      fireEvent.click(screen.getByRole("switch", { name: "Permanent" }));
      save();
      await waitFor(() =>
        expect(update).toHaveBeenCalledWith("t-1", "3", "Acme", {
          team: "storage",
          "notice.paladin.io/content": "Moving to the new backend",
          "notice.paladin.io/url": "https://status.example.com",
          "notice.paladin.io/permanent": "true",
        }),
      );
    });

    it("opens on the notice the tenant has, and clears it", async () => {
      const update = vi.fn().mockResolvedValue(TENANT);
      render(
        <TenantEditDialog
          editing={
            {
              ...TENANT,
              labels: {
                team: "storage",
                "notice.paladin.io/content": "Frozen for audit",
                "notice.paladin.io/severity": "critical",
              },
            } as unknown as Tenant
          }
          onClose={vi.fn()}
          updateTenantMetadata={update}
        />,
      );
      const notice = screen.getByLabelText("Notice");
      expect(notice).toHaveValue("Frozen for audit");
      fireEvent.change(notice, { target: { value: "" } });
      save();
      await waitFor(() =>
        expect(update).toHaveBeenCalledWith("t-1", "3", "Acme", {
          team: "storage",
        }),
      );
    });

    // The console would not show a link it cannot show safely; saying so
    // here beats losing it quietly on save.
    it("holds Save on a link the console would not show", () => {
      open();
      fireEvent.change(screen.getByLabelText("Notice"), {
        target: { value: "x" },
      });
      fireEvent.change(screen.getByLabelText("Link"), {
        target: { value: "javascript:alert(1)" },
      });
      expect(screen.getByText("Fix the link.")).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Save changes" }),
      ).toBeDisabled();
    });
  });
});
