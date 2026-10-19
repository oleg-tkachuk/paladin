import { create } from "@bufbuild/protobuf";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { render, waitFor } from "@/test/utils";
import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

const h = vi.hoisted(() => ({ ops: [] as unknown[] }));
vi.mock("@/context/ShellContext", () => ({
  useShell: () => ({ operations: { status: "ok", data: h.ops } }),
}));

const f = vi.hoisted(() => ({
  badgedIconUrl: vi.fn(async (_src: string, color: string) => `data:${color}`),
}));
vi.mock("@/lib/favicon", async (orig) => ({
  ...(await orig<typeof import("@/lib/favicon")>()),
  badgedIconUrl: f.badgedIconUrl,
  dotColor: (signal: string) => signal,
}));

import { ShellPageTitle } from "./ShellPageTitle";

const OWN_ICON = "/favicon.ico";
const iconHref = () =>
  document.querySelector('link[rel="icon"]')?.getAttribute("href");

describe("ShellPageTitle", () => {
  it("leads the page's title with the user's running operations", async () => {
    h.ops = [create(OperationSchema, { state: OperationState.RUNNING })];
    render(<ShellPageTitle pathname="/users" />);
    await waitFor(() => expect(document.title).toBe("(1) Users · Paladin"));
  });
});

describe("ShellPageTitle's icon", () => {
  beforeEach(() => {
    document.head.innerHTML = `<link rel="icon" href="${OWN_ICON}">`;
    f.badgedIconUrl.mockClear();
  });

  it("dots the icon while an operation runs, and puts it back when none does", async () => {
    h.ops = [create(OperationSchema, { state: OperationState.RUNNING })];
    const { rerender } = render(<ShellPageTitle pathname="/users" />);
    await waitFor(() => expect(iconHref()).toBe("data:running"));
    expect(f.badgedIconUrl).toHaveBeenCalledWith(OWN_ICON, "running");
    h.ops = [create(OperationSchema, { state: OperationState.SUCCEEDED })];
    rerender(<ShellPageTitle pathname="/users" />);
    await waitFor(() => expect(iconHref()).toBe(OWN_ICON));
  });

  it("dots a failure in its own colour, from the plain icon", async () => {
    h.ops = [create(OperationSchema, { state: OperationState.RUNNING })];
    const { rerender } = render(<ShellPageTitle pathname="/users" />);
    await waitFor(() => expect(iconHref()).toBe("data:running"));
    h.ops = [create(OperationSchema, { state: OperationState.FAILED })];
    rerender(<ShellPageTitle pathname="/users" />);
    await waitFor(() => expect(iconHref()).toBe("data:failed"));
    expect(f.badgedIconUrl).toHaveBeenLastCalledWith(OWN_ICON, "failed");
  });

  it("leaves the plain icon while nothing runs", async () => {
    h.ops = [];
    render(<ShellPageTitle pathname="/users" />);
    await waitFor(() => expect(document.title).toBe("Users · Paladin"));
    expect(f.badgedIconUrl).not.toHaveBeenCalled();
    expect(iconHref()).toBe(OWN_ICON);
  });

  it("restores the icon when the shell goes", async () => {
    h.ops = [create(OperationSchema, { state: OperationState.RUNNING })];
    const { unmount } = render(<ShellPageTitle pathname="/users" />);
    await waitFor(() => expect(iconHref()).toBe("data:running"));
    unmount();
    expect(iconHref()).toBe(OWN_ICON);
  });
});
