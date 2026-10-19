import { create } from "@bufbuild/protobuf";
import { describe, expect, it, vi } from "vitest";

import { render, waitFor } from "@/test/utils";
import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

const h = vi.hoisted(() => ({ ops: [] as unknown[] }));
vi.mock("@/context/ShellContext", () => ({
  useShell: () => ({ operations: { status: "ok", data: h.ops } }),
}));

import { ShellPageTitle } from "./ShellPageTitle";

describe("ShellPageTitle", () => {
  it("leads the page's title with the user's running operations", async () => {
    h.ops = [create(OperationSchema, { state: OperationState.RUNNING })];
    render(<ShellPageTitle pathname="/users" />);
    await waitFor(() => expect(document.title).toBe("(1) Users · Paladin"));
  });
});
