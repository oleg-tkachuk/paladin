import { describe, expect, it, vi } from "vitest";
import { UserCircleIcon } from "@heroicons/react/24/outline";

import { fireEvent, render, screen } from "@/test/utils";
import { Table, TableCell, TableRow } from "@/components/ui/table";

import { DetailPage } from "./DetailPage";
import { ListBody, ListPage, SKELETON_ROWS } from "./ListPage";
import { SettingsPage } from "./SettingsPage";

const COLUMNS = 2;
const EMPTY = { icon: UserCircleIcon, what: "users", reason: "empty" as const };

function body(props: Partial<React.ComponentProps<typeof ListBody>>) {
  return render(
    <Table>
      <ListBody
        colSpan={COLUMNS}
        loading={false}
        error={null}
        empty={null}
        {...props}
      >
        <TableRow>
          <TableCell>ada</TableCell>
        </TableRow>
      </ListBody>
    </Table>,
  );
}

describe("ListPage", () => {
  it("lays out the header, the toolbar, the table and what follows", () => {
    render(
      <ListPage
        header={<h1>Users</h1>}
        toolbar={<input aria-label="Search users" />}
        footer={<button type="button">Load more</button>}
      >
        <table aria-label="users" />
      </ListPage>,
    );
    const order = [
      screen.getByRole("heading", { name: "Users" }),
      screen.getByRole("textbox", { name: "Search users" }),
      screen.getByRole("table", { name: "users" }),
      screen.getByRole("button", { name: "Load more" }),
    ];
    for (let i = 1; i < order.length; i++) {
      expect(
        order[i - 1].compareDocumentPosition(order[i]) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    }
  });
});

describe("ListBody", () => {
  it("shows the rows when there are rows", () => {
    body({});
    expect(screen.getByRole("cell", { name: "ada" })).toBeInTheDocument();
  });

  it("shows placeholder rows on the first read, not the rows", () => {
    const { container } = body({ loading: true });
    expect(screen.queryByRole("cell", { name: "ada" })).toBeNull();
    expect(container.querySelectorAll("tbody tr")).toHaveLength(SKELETON_ROWS);
  });

  it("says the read failed where the rows would be, with a retry", () => {
    const onRetry = vi.fn();
    body({
      error: { what: "Users", reason: "unavailable", onRetry },
      empty: EMPTY,
    });
    expect(screen.getByText(/unavailable/)).toBeInTheDocument();
    expect(screen.queryByText(/No users/i)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(onRetry).toHaveBeenCalled();
  });

  it("says there are none, across every column", () => {
    const { container } = body({ empty: EMPTY });
    expect(screen.queryByRole("cell", { name: "ada" })).toBeNull();
    expect(container.querySelector("td")).toHaveAttribute(
      "colspan",
      String(COLUMNS),
    );
  });

  // A failed read never reads as an empty list, nor a loading one as either.
  it("puts loading before the failure, and the failure before empty", () => {
    const error = { what: "Users", reason: "unavailable", onRetry: vi.fn() };
    const { container } = body({ loading: true, error, empty: EMPTY });
    expect(container.querySelectorAll("tbody tr")).toHaveLength(SKELETON_ROWS);
    expect(screen.queryByText(/unavailable/)).toBeNull();
  });
});

describe("DetailPage", () => {
  const header = <h1>Ada</h1>;
  it("holds a placeholder until the resource is read", () => {
    render(
      <DetailPage header={header} pending error={null}>
        <p>roles</p>
      </DetailPage>,
    );
    expect(screen.getByRole("heading", { name: "Ada" })).toBeInTheDocument();
    expect(screen.queryByText("roles")).toBeNull();
  });

  it("puts a failed read in place of the resource", () => {
    render(
      <DetailPage
        header={header}
        pending={false}
        error={{ what: "User", reason: "not found", onRetry: vi.fn() }}
      >
        <p>roles</p>
      </DetailPage>,
    );
    expect(screen.getByText(/not found/)).toBeInTheDocument();
    expect(screen.queryByText("roles")).toBeNull();
  });

  it("shows the resource once read", () => {
    render(
      <DetailPage header={header} pending={false} error={null}>
        <p>roles</p>
      </DetailPage>,
    );
    expect(screen.getByText("roles")).toBeInTheDocument();
  });
});

describe("SettingsPage", () => {
  it("puts the summary above the sections", () => {
    render(
      <SettingsPage header={<h1>Profile</h1>} summary={<p>Ada</p>}>
        <section aria-label="Preferences" />
        <section aria-label="Password" />
      </SettingsPage>,
    );
    const summary = screen.getByText("Ada");
    const sections = screen.getAllByRole("region");
    expect(sections).toHaveLength(2);
    expect(
      summary.compareDocumentPosition(sections[0]) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(sections[0].parentElement).toBe(sections[1].parentElement);
  });
});
