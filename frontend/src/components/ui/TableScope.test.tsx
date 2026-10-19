import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, renderHook, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  stored: {} as Record<string, unknown>,
  updateMine: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  userSettingsClient: {
    getMine: async () => ({ preferences: { ...h.stored } }),
    updateMine: h.updateMine,
  },
}));

import { Table, TableBody, TableCell, TableRow } from "./table";
import { TableScope } from "./TableScope";
import { TablePrefsMenu } from "./TablePrefsMenu";
import { useTableSort } from "@/hooks/useTableSort";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { USER_SETTINGS_QUERY_KEY } from "@/lib/theme";

function clientWith(stored: Record<string, unknown>) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  // As the shell leaves it after sign-in: the settings already loaded.
  client.setQueryData(USER_SETTINGS_QUERY_KEY, { preferences: stored });
  return client;
}

const COLUMNS = ["name", "created"] as const;

beforeEach(() => {
  h.stored = {};
  h.updateMine.mockReset().mockImplementation(async ({ preferences }) => {
    h.stored = { ...preferences };
    return { preferences: { ...h.stored } };
  });
  window.history.replaceState(null, "", "/list");
});

function Scoped({ children }: { children: ReactNode }) {
  return (
    <TableScope id="users" sort={{ column: "created", direction: "desc" }}>
      <TablePrefsMenu />
      <Table>
        <TableBody>
          <TableRow>
            <TableCell>row</TableCell>
          </TableRow>
        </TableBody>
      </Table>
      {children}
    </TableScope>
  );
}

describe("TableScope", () => {
  it("sets the table at the density the user chose for it", () => {
    h.stored = { [PREFERENCE_KEYS.tables]: { users: { density: "compact" } } };
    render(
      <QueryClientProvider client={clientWith(h.stored)}>
        <Scoped>{null}</Scoped>
      </QueryClientProvider>,
    );
    expect(screen.getByRole("table")).toHaveAttribute(
      "data-density",
      "compact",
    );
  });

  it("saves the density and the sort on screen from the menu", async () => {
    const user = userEvent.setup();
    h.stored = { other: { keep: 1 } };
    render(
      <QueryClientProvider client={clientWith(h.stored)}>
        <Scoped>{null}</Scoped>
      </QueryClientProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Table settings" }));
    await user.click(
      await screen.findByRole("menuitemradio", { name: "Compact" }),
    );
    await waitFor(() =>
      expect(h.stored[PREFERENCE_KEYS.tables]).toEqual({
        users: { density: "compact" },
      }),
    );
    expect(h.stored.other).toEqual({ keep: 1 });

    await user.click(screen.getByRole("button", { name: "Table settings" }));
    await user.click(
      await screen.findByRole("menuitem", { name: "Open with this sort" }),
    );
    await waitFor(() =>
      expect(h.stored[PREFERENCE_KEYS.tables]).toEqual({
        users: {
          density: "compact",
          sort: { column: "created", direction: "desc" },
        },
      }),
    );
  });

  it("keeps the defaults and offers no menu without a query client", () => {
    render(<Scoped>{null}</Scoped>);
    expect(screen.getByRole("table")).toHaveAttribute(
      "data-density",
      "comfortable",
    );
    expect(screen.queryByRole("button", { name: "Table settings" })).toBeNull();
  });
});

describe("useTableSort with a saved default", () => {
  const saved = {
    [PREFERENCE_KEYS.tables]: {
      users: { sort: { column: "created", direction: "desc" } },
    },
  };
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={clientWith(saved)}>
      {children}
    </QueryClientProvider>
  );
  const inUrl = { url: PAGE_TABLE, columns: COLUMNS, tableId: "users" };

  it("opens with the saved sort when the URL names none", () => {
    const { result } = renderHook(() => useTableSort(undefined, inUrl), {
      wrapper,
    });
    expect(result.current.sort).toEqual({
      column: "created",
      direction: "desc",
    });
  });

  it("lets a link's sort win over the saved one", () => {
    window.history.replaceState(null, "", "/list?sort=name&dir=asc");
    const { result } = renderHook(() => useTableSort(undefined, inUrl), {
      wrapper,
    });
    expect(result.current.sort).toEqual({ column: "name", direction: "asc" });
  });
});
