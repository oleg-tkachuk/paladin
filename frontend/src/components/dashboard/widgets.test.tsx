import { create } from "@bufbuild/protobuf";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { render, screen } from "@/test/utils";
import { GetPlatformStatsResponseSchema } from "@/gen/paladin/admin/v1/system_service_pb";
import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";
import { BOOTSTRAP_TENANT_COUNT } from "@/lib/firstRun";

const h = vi.hoisted(() => ({
  summary: {
    data: undefined as unknown,
    isPending: false,
    error: null as unknown,
  },
  operations: {
    status: "ok",
    data: [] as unknown[],
    reason: undefined as string | undefined,
  },
}));
vi.mock("./usePlatformSummary", () => ({
  usePlatformSummary: () => h.summary,
}));
vi.mock("@/context/ShellContext", () => ({
  useShell: () => ({ operations: h.operations }),
}));

import { FirstRunChecklist } from "./FirstRunChecklist";
import { MY_OPERATIONS_SHOWN, MyOperationsWidget } from "./MyOperationsWidget";
import { QuotaAlertsWidget } from "./QuotaAlertsWidget";

const n = BigInt;
const stats = (o: {
  backends: number;
  buckets: number;
  atLimit?: number;
  nearLimit?: number;
}) =>
  create(GetPlatformStatsResponseSchema, {
    backends: { total: n(o.backends) },
    tenants: { total: n(BOOTSTRAP_TENANT_COUNT + 1) },
    buckets: { total: n(o.buckets) },
    collections: { total: n(1) },
    rls: {
      available: true,
      objects: { totalCount: n(1) },
      apiTokens: { total: n(1) },
      quotas: { atLimit: n(o.atLimit ?? 0), nearLimit: n(o.nearLimit ?? 0) },
    },
  });

beforeEach(() => {
  h.summary = { data: undefined, isPending: false, error: null };
  h.operations = { status: "ok", data: [], reason: undefined };
});

describe("FirstRunChecklist", () => {
  it("leads to what is left, and counts what is done", () => {
    h.summary.data = stats({ backends: 1, buckets: 0 });
    render(<FirstRunChecklist />);
    expect(screen.getByText(/Getting started · 5 of 6/)).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /Create a bucket/ }),
    ).toHaveAttribute("href", "/buckets");
  });

  it("is gone once everything is done, or when it cannot count", () => {
    h.summary.data = stats({ backends: 1, buckets: 1 });
    const { rerender } = render(<FirstRunChecklist />);
    expect(screen.queryByTestId("first-run")).toBeNull();
    h.summary = { data: undefined, isPending: false, error: new Error("down") };
    rerender(<FirstRunChecklist />);
    expect(screen.queryByTestId("first-run")).toBeNull();
  });
});

describe("MyOperationsWidget", () => {
  const op = (i: number, state: OperationState) =>
    create(OperationSchema, { name: `operations/${i}`, state });

  it(`lists the latest ${MY_OPERATIONS_SHOWN} with their state`, () => {
    h.operations.data = Array.from(
      { length: MY_OPERATIONS_SHOWN + 2 },
      (_, i) =>
        op(i, i === 0 ? OperationState.FAILED : OperationState.SUCCEEDED),
    );
    render(<MyOperationsWidget />);
    expect(screen.getAllByRole("listitem")).toHaveLength(MY_OPERATIONS_SHOWN);
    expect(screen.getByText("failed")).toBeInTheDocument();
  });

  it("says why when the operations cannot be read", () => {
    h.operations = {
      status: "unavailable",
      data: [],
      reason: "admin plane down",
    };
    render(<MyOperationsWidget />);
    expect(screen.getByText(/admin plane down/)).toBeInTheDocument();
  });
});

describe("QuotaAlertsWidget", () => {
  it("counts the quotas at and near their limits", () => {
    h.summary.data = stats({
      backends: 1,
      buckets: 1,
      atLimit: 2,
      nearLimit: 3,
    });
    render(<QuotaAlertsWidget />);
    expect(screen.getByText("2 at their limit")).toBeInTheDocument();
    expect(screen.getByText("3 near it")).toBeInTheDocument();
  });

  it("says when none is near", () => {
    h.summary.data = stats({ backends: 1, buckets: 1 });
    render(<QuotaAlertsWidget />);
    expect(screen.getByText(/No quota is near its limit/)).toBeInTheDocument();
  });
});
