import {
  QueryClient,
  QueryClientProvider,
  useQuery,
} from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { IntentTableRow } from "@/components/ui/IntentTableRow";
import { Table, TableBody, TableCell } from "@/components/ui/table";
import { LOADING_BAR_DELAY_MS } from "@/lib/loadingBar";
import { LoadingBar } from "./LoadingBar";

afterEach(() => {
  vi.useRealTimers();
});

const never = () => new Promise<never>(() => {});

function Loader({
  queryKey,
  fn,
}: {
  queryKey: string;
  fn: () => Promise<unknown>;
}) {
  useQuery({ queryKey: [queryKey], queryFn: fn });
  return null;
}

const bar = () => screen.getByRole("progressbar", { hidden: true });

describe("LoadingBar", () => {
  it("shows once a first load outlasts the delay", () => {
    vi.useFakeTimers();
    const client = new QueryClient();
    render(
      <QueryClientProvider client={client}>
        <LoadingBar />
        <Loader queryKey="a" fn={never} />
      </QueryClientProvider>,
    );
    // The query client batches its notices on a zero timer.
    act(() => vi.advanceTimersByTime(0));
    expect(bar()).toHaveAttribute("aria-hidden", "true");
    act(() => vi.advanceTimersByTime(LOADING_BAR_DELAY_MS + 1));
    expect(bar()).toHaveAttribute("aria-hidden", "false");
  });

  // A poll refreshing data already on screen is not a wait for the user.
  it("stays hidden while data on screen refreshes in the background", () => {
    vi.useFakeTimers();
    const client = new QueryClient();
    client.setQueryData(["a"], "shown");
    client.invalidateQueries({ queryKey: ["a"] });
    render(
      <QueryClientProvider client={client}>
        <LoadingBar />
        <Loader queryKey="a" fn={never} />
      </QueryClientProvider>,
    );
    act(() => vi.advanceTimersByTime(0));
    // The refresh is under way, and the bar still keeps out of it.
    expect(client.isFetching()).toBeGreaterThan(0);
    act(() => vi.advanceTimersByTime(LOADING_BAR_DELAY_MS * 4));
    expect(bar()).toHaveAttribute("aria-hidden", "true");
  });
});

describe("IntentTableRow", () => {
  it("warms its page's query when pointed at, once", async () => {
    const client = new QueryClient();
    const fn = vi.fn(async () => "detail");
    render(
      <QueryClientProvider client={client}>
        <Table>
          <TableBody>
            <IntentTableRow query={{ queryKey: ["detail", 1], queryFn: fn }}>
              <TableCell>row</TableCell>
            </IntentTableRow>
          </TableBody>
        </Table>
      </QueryClientProvider>,
    );
    const row = screen.getByText("row").closest("tr")!;
    fireEvent.mouseEnter(row);
    fireEvent.focus(row);
    fireEvent.mouseEnter(row);
    await act(async () => {});
    expect(fn).toHaveBeenCalledTimes(1);
    expect(client.getQueryData(["detail", 1])).toBe("detail");
  });
});
