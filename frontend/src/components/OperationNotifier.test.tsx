import { create } from "@bufbuild/protobuf";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { render } from "@/test/utils";
import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

const h = vi.hoisted(() => ({
  ops: [] as unknown[],
  enabled: true,
  shown: [] as { title: string; options: NotificationOptions }[],
}));
vi.mock("@/context/ShellContext", () => ({
  useShell: () => ({ operations: { status: "ok", data: h.ops } }),
}));
vi.mock("@/hooks/usePreference", () => ({
  usePreference: () => [h.enabled, vi.fn()],
}));
vi.mock("next/navigation", async (importOriginal) => ({
  ...(await importOriginal<typeof import("next/navigation")>()),
  useRouter: () => ({ push: vi.fn() }),
}));

import { OperationNotifier } from "./OperationNotifier";

class FakeNotification {
  static permission = "granted";
  onclick: (() => void) | null = null;
  constructor(title: string, options: NotificationOptions) {
    h.shown.push({ title, options });
  }
  close() {}
}

const op = (state: OperationState) =>
  create(OperationSchema, {
    name: "operations/1",
    state,
    done: state > OperationState.RUNNING,
  });

function setHidden(hidden: boolean) {
  Object.defineProperty(document, "hidden", {
    configurable: true,
    get: () => hidden,
  });
}

beforeEach(() => {
  vi.stubGlobal("Notification", FakeNotification);
  h.shown = [];
  h.enabled = true;
  setHidden(true);
});
afterEach(() => {
  vi.unstubAllGlobals();
  setHidden(false);
});

describe("OperationNotifier", () => {
  it("notifies once when a running operation finishes in a hidden tab", () => {
    h.ops = [op(OperationState.RUNNING)];
    const { rerender } = render(<OperationNotifier />);
    expect(h.shown).toHaveLength(0);
    h.ops = [op(OperationState.FAILED)];
    rerender(<OperationNotifier />);
    expect(h.shown).toHaveLength(1);
    expect(h.shown[0].title).toMatch(/failed$/);
    expect(h.shown[0].options.tag).toBe("operations/1");
    h.ops = [op(OperationState.FAILED)];
    rerender(<OperationNotifier />);
    expect(h.shown).toHaveLength(1);
  });

  it.each([
    ["the tab is in front", () => setHidden(false)],
    ["the user did not ask", () => (h.enabled = false)],
    [
      "the browser does not allow it",
      () => (FakeNotification.permission = "denied"),
    ],
  ])("says nothing when %s", (_, arrange) => {
    arrange();
    h.ops = [op(OperationState.RUNNING)];
    const { rerender } = render(<OperationNotifier />);
    h.ops = [op(OperationState.SUCCEEDED)];
    rerender(<OperationNotifier />);
    expect(h.shown).toHaveLength(0);
    FakeNotification.permission = "granted";
  });
});
