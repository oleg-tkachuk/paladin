import type { ReactElement, ReactNode } from "react";
import {
  render as rtlRender,
  type RenderOptions,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

// Re-export the RTL helpers the suites use so a test only needs one import.
export {
  screen,
  fireEvent,
  waitFor,
  within,
  act,
  cleanup,
} from "@testing-library/react";

/**
 * render() wrapped in a fresh QueryClientProvider so components that use
 * useQuery / useInfiniteQuery work under test (otherwise they throw "No
 * QueryClient set"). A new client per render keeps tests isolated; retries
 * off + gcTime 0 keep them deterministic.
 */
export function render(
  ui: ReactElement,
  options?: Omit<RenderOptions, "wrapper">,
) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return rtlRender(ui, { wrapper: Wrapper, ...options });
}
