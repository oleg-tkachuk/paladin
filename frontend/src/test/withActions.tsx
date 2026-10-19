import { act } from "@testing-library/react";
import type React from "react";
import { vi } from "vitest";

import { ActionsProvider, useActions } from "@/context/ActionsContext";

import { render } from "./utils";

/**
 * Renders a page inside the palette's registry, reporting what it offers.
 * The registry's toasts need the Notification mock the page's own test sets.
 */
export function renderWithActions(ui: React.ReactElement) {
  const seen = vi.fn<(registry: ReturnType<typeof useActions>) => void>();
  function Watch() {
    seen(useActions());
    return null;
  }
  const view = render(
    <ActionsProvider>
      {ui}
      <Watch />
    </ActionsProvider>,
  );
  const registry = () => seen.mock.lastCall![0];
  return {
    ...view,
    /** The ids of the actions offered now. */
    actionIds: () => registry().actions.map((a) => a.id),
    /** Selects an action, as the palette does. */
    run: (id: string) => act(() => registry().executeAction(id)),
  };
}
