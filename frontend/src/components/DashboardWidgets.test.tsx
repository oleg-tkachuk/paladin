import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { onlineManager } from "@tanstack/react-query";
import { render, screen } from "@/test/utils";
import { Code, ConnectError } from "@connectrpc/connect";
import { CAPABILITIES_DOCS_URL } from "@/constants";
import { create, fromJson } from "@bufbuild/protobuf";
import { anyPack, StructSchema } from "@bufbuild/protobuf/wkt";
import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

const h = vi.hoisted(() => ({
  audit: vi.fn(),
  ops: vi.fn(),
  budget: vi.fn(),
  tenants: vi.fn(),
  series: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  auditClient: { listAuditLog: h.audit },
  adminOperationClient: { listOperations: h.ops },
  tenantBudgetClient: { summarize: h.budget },
  tenantClient: { listTenants: h.tenants },
  billingClient: { listTenantTimeSeries: h.series },
}));

import { DashboardWidgets, TREND_LABEL } from "./DashboardWidgets";

const EMPTY_STATES = [
  "No recent activity.",
  "No failures in the recent window.",
  "No tenants are approaching their cap.",
];

describe("DashboardWidgets", () => {
  beforeEach(() => {
    h.audit.mockReset();
    h.ops.mockReset();
    h.budget.mockReset();
    h.tenants.mockReset();
    h.tenants.mockResolvedValue({ tenants: [], page: {} });
    h.series.mockReset();
    h.series.mockResolvedValue({ series: [] });
  });
  afterEach(() => onlineManager.setOnline(true));

  // How each tenant near its budget got there: one read for every row's
  // trend, a line where it spent, none where it did not.
  it("draws a spend trend beside each budget alert that spent", async () => {
    h.audit.mockResolvedValue({ entries: [] });
    h.ops.mockResolvedValue({ operations: [] });
    const near = (id: string, name: string) => ({
      tenantId: id,
      slug: id,
      displayName: name,
      utilisationPct: 92,
      budget: {},
    });
    h.budget.mockResolvedValue({
      summaries: [near("t-busy", "Busy"), near("t-quiet", "Quiet")],
    });
    h.series.mockResolvedValue({
      series: [
        {
          tenantId: "t-busy",
          buckets: [
            {
              start: {
                seconds: BigInt(Math.floor(Date.now() / 1000)) - 86_400n,
                nanos: 0,
              },
              chargeCount: 2n,
              spent: { currencyCode: "USD", units: 3n, nanos: 0 },
            },
          ],
        },
        { tenantId: "t-quiet", buckets: [] },
      ],
    });

    render(<DashboardWidgets />);

    const lines = await screen.findAllByRole("img", { name: TREND_LABEL });
    expect(lines).toHaveLength(1);
    expect(lines[0].closest("a")?.textContent).toContain("Busy");
    expect(h.series).toHaveBeenCalledTimes(1);
    expect(h.series).toHaveBeenCalledWith(
      expect.objectContaining({
        tenantIds: ["t-busy", "t-quiet"],
        granularity: "day",
      }),
      expect.anything(),
    );
  });

  it("shows the empty states when every query answers with nothing", async () => {
    h.audit.mockResolvedValue({ entries: [] });
    h.ops.mockResolvedValue({ operations: [] });
    h.budget.mockResolvedValue({ summaries: [] });

    render(<DashboardWidgets />);

    for (const text of EMPTY_STATES) {
      expect(await screen.findByText(text)).toBeInTheDocument();
    }
  });

  // A paused query (offline, or a retry held back while the window is
  // unfocused) has no answer yet; "no failures" would be a claim.
  it("does not claim an empty result while the queries are paused", () => {
    onlineManager.setOnline(false);

    render(<DashboardWidgets />);

    for (const text of EMPTY_STATES) {
      expect(screen.queryByText(text)).toBeNull();
    }
  });

  // A batch that ran to its end leaving objects behind is a failure: asked
  // for by the failed view's filter, and shown by what it could not do.
  it("lists a batch that left objects behind among the failures", async () => {
    h.audit.mockResolvedValue({ entries: [] });
    h.budget.mockResolvedValue({ summaries: [] });
    h.ops.mockResolvedValue({
      operations: [
        create(OperationSchema, {
          name: "operations/p-1",
          type: "BatchDelete",
          state: OperationState.SUCCEEDED,
          failedCount: 1n,
          result: {
            case: "response",
            value: anyPack(
              StructSchema,
              fromJson(StructSchema, { total: 2, succeeded: 1, failed: 1 }),
            ),
          },
        }),
        create(OperationSchema, {
          name: "operations/c-1",
          type: "BatchCopy",
          state: OperationState.SUCCEEDED,
        }),
      ],
    });

    render(<DashboardWidgets />);

    expect(await screen.findByText("BatchDelete")).toBeInTheDocument();
    expect(screen.getByText(/1 failed/)).toBeInTheDocument();
    expect(screen.queryByText("BatchCopy")).toBeNull();
    expect(h.ops).toHaveBeenCalledWith(
      expect.objectContaining({ filter: "has_failures" }),
      expect.anything(),
    );
  });

  it("says a widget is unavailable when its query fails", async () => {
    h.audit.mockRejectedValue(new Error("fetch failed"));
    h.ops.mockRejectedValue(new Error("fetch failed"));
    h.budget.mockRejectedValue(new Error("unavailable"));

    render(<DashboardWidgets />);

    expect(
      await screen.findByText("Activity unavailable."),
    ).toBeInTheDocument();
    expect(
      await screen.findByText("Operations unavailable."),
    ).toBeInTheDocument();
    for (const text of EMPTY_STATES) {
      expect(screen.queryByText(text)).toBeNull();
    }
  });

  // It used to guess "capability subsystem may be disabled" for any failure.
  it("says why the budget summary is unavailable and how to enable it", async () => {
    h.audit.mockResolvedValue({ entries: [] });
    h.ops.mockResolvedValue({ operations: [] });
    h.budget.mockRejectedValue(
      new ConnectError(
        "capability subsystem disabled; tenant budget unavailable",
        Code.Unavailable,
      ),
    );

    render(<DashboardWidgets />);

    expect(
      await screen.findByText(
        "Summary unavailable: capability subsystem disabled; tenant budget unavailable",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "How to enable capabilities" }),
    ).toHaveAttribute("href", CAPABILITIES_DOCS_URL);
  });

  describe("trash past retention", () => {
    beforeEach(() => {
      h.audit.mockResolvedValue({ entries: [] });
      h.ops.mockResolvedValue({ operations: [] });
      h.budget.mockResolvedValue({ summaries: [] });
    });

    it("asks the server for trashed tenants past their retention", async () => {
      render(<DashboardWidgets />);
      await screen.findByText("No recent activity.");
      expect(h.tenants).toHaveBeenCalledWith(
        expect.objectContaining({
          onlyTrashed: true,
          filter: "purge_eligible",
        }),
        expect.anything(),
      );
    });

    it("says how many and links to the trash", async () => {
      h.tenants.mockResolvedValue({
        tenants: [{ tenantId: "a" }, { tenantId: "b" }],
        page: {},
      });
      render(<DashboardWidgets />);
      const notice = await screen.findByRole("status");
      expect(notice).toHaveTextContent(
        /2 tenants are in the trash past their retention/,
      );
      expect(notice).toHaveAttribute("href", "/trash");
    });

    it("says there are more when the page is full", async () => {
      h.tenants.mockResolvedValue({
        tenants: [{ tenantId: "a" }],
        page: { nextPageToken: "next" },
      });
      render(<DashboardWidgets />);
      expect(await screen.findByRole("status")).toHaveTextContent(
        /1\+ tenants are/,
      );
    });

    it("shows nothing when there are none, or the read fails", async () => {
      render(<DashboardWidgets />);
      await screen.findByText("No recent activity.");
      expect(screen.queryByRole("status")).toBeNull();

      h.tenants.mockRejectedValue(new ConnectError("down", Code.Unavailable));
      render(<DashboardWidgets />);
      await screen.findAllByText("No recent activity.");
      expect(screen.queryByRole("status")).toBeNull();
    });
  });
});
