import { act, render, renderHook } from "@testing-library/react";
import type React from "react";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

import {
  ActionsProvider,
  useActions,
  usePageAction,
  type PageAction,
} from "./ActionsContext";

const wrapper = ({ children }: { children: React.ReactNode }) => (
  <ActionsProvider>{children}</ActionsProvider>
);

const create = (perform: () => void): PageAction => ({
  id: "create.bucket",
  label: "New bucket",
  category: "create",
  perform,
});

function useBoth(action: PageAction | null) {
  usePageAction(action);
  return useActions();
}

describe("usePageAction", () => {
  it("offers the action while the page is mounted, and withdraws it after", () => {
    // The page is a child of the registry, so it can go while the registry stays.
    function Page() {
      usePageAction(create(vi.fn()));
      return null;
    }
    const seen = vi.fn();
    function Watch() {
      seen(useActions().actions.map((a) => a.id));
      return null;
    }
    const view = render(
      <ActionsProvider>
        <Page />
        <Watch />
      </ActionsProvider>,
    );
    expect(seen).toHaveBeenLastCalledWith(["create.bucket"]);
    view.rerender(
      <ActionsProvider>
        <Watch />
      </ActionsProvider>,
    );
    expect(seen).toHaveBeenLastCalledWith([]);
  });

  it("offers nothing for a create the page has disabled", () => {
    const { result } = renderHook(() => useBoth(null), { wrapper });
    expect(result.current.actions).toEqual([]);
  });

  it("runs the page's latest perform, not the one it first registered", async () => {
    const first = vi.fn();
    const latest = vi.fn();
    const { result, rerender } = renderHook(
      ({ perform }: { perform: () => void }) => useBoth(create(perform)),
      { wrapper, initialProps: { perform: first } },
    );
    rerender({ perform: latest });
    await act(() => result.current.executeAction("create.bucket"));
    expect(latest).toHaveBeenCalledTimes(1);
    expect(first).not.toHaveBeenCalled();
  });

  it("does nothing on a page rendered without the registry", () => {
    expect(() =>
      renderHook(() => usePageAction(create(vi.fn()))),
    ).not.toThrow();
  });
});

// The objects page registers its Undo under one id after every bulk trash;
// the registry kept the first and ignored the rest, so a second trash's Undo
// restored the first batch.
describe("registerAction", () => {
  it("replaces an action registered again under the same id", async () => {
    const first = vi.fn();
    const second = vi.fn();
    const { result } = renderHook(() => useActions(), { wrapper });
    act(() =>
      result.current.registerAction({
        id: "objects.bulk_restore_last",
        label: "Undo: Restore Objects",
        category: "operation",
        perform: first,
      }),
    );
    act(() =>
      result.current.registerAction({
        id: "objects.bulk_restore_last",
        label: "Undo: Restore Objects",
        category: "operation",
        perform: second,
      }),
    );
    expect(result.current.actions).toHaveLength(1);
    await act(() => result.current.executeAction("objects.bulk_restore_last"));
    expect(second).toHaveBeenCalledTimes(1);
    expect(first).not.toHaveBeenCalled();
  });
});
