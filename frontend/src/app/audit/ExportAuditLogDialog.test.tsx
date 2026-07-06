import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

const h = vi.hoisted(() => ({ export: vi.fn(), notify: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  auditClient: { exportAuditLog: h.export },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));

import { ExportAuditLogDialog } from "./ExportAuditLogDialog";

describe("ExportAuditLogDialog", () => {
  beforeEach(() => {
    h.export.mockReset();
    h.notify.mockReset();
  });

  it("requires a destination, then starts the export with filter + destination", async () => {
    h.export.mockResolvedValue({ name: "operations/exp-1" });
    render(
      <ExportAuditLogDialog
        open
        onOpenChange={() => {}}
        initialFilter='action.startsWith("admin.")'
      />,
    );

    const start = await screen.findByRole("button", { name: /start export/i });
    // Guard: disabled with an empty destination.
    expect(start).toBeDisabled();

    await userEvent.type(
      screen.getByLabelText(/destination/i),
      "audit/2026-07.ndjson",
    );
    expect(start).toBeEnabled();
    await userEvent.click(start);

    await waitFor(() => expect(h.export).toHaveBeenCalledTimes(1));
    expect(h.export).toHaveBeenCalledWith({
      filter: 'action.startsWith("admin.")',
      destination: "audit/2026-07.ndjson",
    });
    // Surfaces the returned operation name so the operator can track it.
    expect(h.notify).toHaveBeenCalledWith(
      expect.objectContaining({
        type: "success",
        message: "operations/exp-1",
      }),
    );
  });

  it("surfaces an export failure via notification and stays open", async () => {
    h.export.mockRejectedValue(new Error("nope"));
    const onOpenChange = vi.fn();
    render(<ExportAuditLogDialog open onOpenChange={onOpenChange} />);

    await userEvent.type(
      screen.getByLabelText(/destination/i),
      "audit/x.ndjson",
    );
    await userEvent.click(
      screen.getByRole("button", { name: /start export/i }),
    );

    await waitFor(() => expect(h.export).toHaveBeenCalledTimes(1));
    expect(h.notify).toHaveBeenCalledWith(
      expect.objectContaining({ type: "error" }),
    );
    // Failure keeps the dialog open (no onDone → no close call).
    expect(onOpenChange).not.toHaveBeenCalled();
  });
});
