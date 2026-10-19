import { describe, expect, it } from "vitest";

import { render, screen, waitFor } from "@/test/utils";
import {
  LINKED_ROW_ATTR,
  NUMERIC_ATTR,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./table";

function Listing({ rows }: { rows: string[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Size</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={r}>
            <TableCell>{r}</TableCell>
            <TableCell>1 KB</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

// At phone width a row is a card of "column: value"; the cells carry the
// column's name for it, rows added later included.
describe("Table cell labels", () => {
  it("names every cell by its column, and the rows that arrive later", async () => {
    const { rerender } = render(<Listing rows={["a.txt"]} />);
    expect(screen.getByText("a.txt")).toHaveAttribute("data-label", "Name");
    rerender(<Listing rows={["a.txt", "b.txt"]} />);
    await waitFor(() =>
      expect(screen.getByText("b.txt")).toHaveAttribute("data-label", "Name"),
    );
    expect(screen.getAllByText("1 KB")[1]).toHaveAttribute(
      "data-label",
      "Size",
    );
  });
});

describe("TableRow linked", () => {
  // Tailwind sees only literal class text, so the highlight names the
  // attribute itself; this holds it to the constant the hook sets.
  it("highlights the row carrying the linked attribute", () => {
    render(
      <table>
        <tbody>
          <TableRow {...{ [LINKED_ROW_ATTR]: true }}>
            <td>r</td>
          </TableRow>
        </tbody>
      </table>,
    );
    const row = screen.getByRole("row");
    expect(row).toHaveAttribute(LINKED_ROW_ATTR);
    expect(row.className).toContain(`${LINKED_ROW_ATTR}:bg-primary`);
  });
});

describe("numeric cells", () => {
  it("aligns a column of figures, head and cells alike", () => {
    render(
      <table>
        <thead>
          <tr>
            <TableHead numeric>Size</TableHead>
            <TableHead>Name</TableHead>
          </tr>
        </thead>
        <tbody>
          <tr>
            <TableCell numeric>12</TableCell>
            <TableCell>a</TableCell>
          </tr>
        </tbody>
      </table>,
    );
    for (const el of [
      screen.getByRole("columnheader", { name: "Size" }),
      screen.getByRole("cell", { name: "12" }),
    ]) {
      expect(el).toHaveClass("text-right", "tabular-nums");
      expect(el).toHaveAttribute(NUMERIC_ATTR);
    }
    expect(screen.getByRole("cell", { name: "a" })).not.toHaveClass(
      "text-right",
    );
    expect(
      screen.getByRole("columnheader", { name: "Name" }),
    ).not.toHaveAttribute(NUMERIC_ATTR);
  });
});
