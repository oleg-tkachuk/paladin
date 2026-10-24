import { beforeEach, describe, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";
import { render, screen, waitFor, within } from "@/test/utils";

const h = vi.hoisted(() => ({
  listAuditLog: vi.fn(),
  listOperations: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  auditClient: { listAuditLog: h.listAuditLog },
  adminOperationClient: { listOperations: h.listOperations },
  userClient: { getUser: vi.fn() },
}));
vi.mock("../tenant-context", () => ({
  useTenant: () => ({ tenantId: "t-1", slug: "acme", displayName: "Acme" }),
}));

import TenantHistoryPage from "./page";

const at = (iso: string) => timestampFromDate(new Date(iso));

beforeEach(() => {
  h.listAuditLog.mockReset().mockResolvedValue({
    entries: [
      {
        entryId: "e-1",
        at: at("2026-10-20T09:00:00Z"),
        action: "/paladin.admin.v1.TenantService/UpdateTenant",
        actorSubject: "apikey:7c2a",
        actorTenantId: "t-1",
        errorMessage: "",
      },
      {
        entryId: "e-2",
        at: at("2026-10-20T08:00:00Z"),
        action: "/paladin.admin.v1.QuotaService/SetQuota",
        actorSubject: "",
        actorTenantId: "",
        errorMessage: "failed_precondition: quota changed elsewhere",
      },
    ],
  });
  h.listOperations.mockReset().mockResolvedValue({
    operations: [
      create(OperationSchema, {
        name: "operations/op-1",
        type: "BatchCopy",
        state: OperationState.RUNNING,
        createdAt: at("2026-10-20T10:00:00Z"),
        initiatorSubject: "system:worker",
        initiatorTenantId: "t-1",
      }),
    ],
  });
});

describe("TenantHistoryPage", () => {
  it("reads the tenant's trail and its own operations", async () => {
    render(<TenantHistoryPage />);
    await waitFor(() => expect(h.listOperations).toHaveBeenCalled());
    expect(h.listAuditLog).toHaveBeenCalledWith(
      expect.objectContaining({ tenantId: "t-1", filter: "" }),
      expect.anything(),
    );
    // Named, so a platform admin on another tenant's page reads that
    // tenant's batches; filtered too, so a server from before tenant_id —
    // mid-rollout — cannot show the caller's own tenant's as this one's.
    expect(h.listOperations).toHaveBeenCalledWith(
      expect.objectContaining({
        tenantId: "t-1",
        filter: 'tenant_id == "t-1"',
      }),
      expect.anything(),
    );
  });

  // After Argo CD's deployment history: newest first, each with its outcome,
  // its actor's kind and the record it came from.
  it("shows one timeline, newest first, outcome and actor on each", async () => {
    render(<TenantHistoryPage />);
    const list = await screen.findByRole("list", { name: "History" });
    await waitFor(() =>
      expect(within(list).getAllByRole("listitem")).toHaveLength(3),
    );
    const [op, change, failed] = within(list).getAllByRole("listitem");

    expect(op).toHaveTextContent("Copy objects");
    expect(within(op).getByRole("img", { name: "Running" })).toBeVisible();
    expect(op).toHaveTextContent("System");
    expect(within(op).getByRole("link")).toHaveAttribute(
      "href",
      "/operations?id=operations%2Fop-1",
    );

    expect(change).toHaveTextContent("Update tenant");
    expect(change).toHaveTextContent("Automated");
    expect(within(change).getByRole("link")).toHaveAttribute(
      "href",
      "/audit?id=e-1",
    );

    expect(within(failed).getByRole("img", { name: "Failed" })).toBeVisible();
    expect(failed).toHaveTextContent("quota changed elsewhere");
    // An entry with no actor is Paladin's own.
    expect(failed).toHaveTextContent("Paladin");
  });

  it("says the history failed rather than that there is none", async () => {
    h.listAuditLog.mockRejectedValue(new Error("unavailable: upstream"));
    render(<TenantHistoryPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /History could not be loaded/,
    );
    expect(screen.queryByText(/No changes/)).toBeNull();
  });
});
