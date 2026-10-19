import { describe, expect, it } from "vitest";

import { render, screen, waitFor } from "@/test/utils";
import {
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
