import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";

import { Table, TableBody, TableCell, TableRow } from "./table";

// Every console table renders through this component, and the sidebar takes a
// fixed share of the window: a column shown at a viewport breakpoint counted
// width its table never got, and pushed Status and the actions menus past the
// card's edge on nine pages. Columns are keyed to the table's own width.
describe("Table", () => {
  it("is a size container its columns can query", () => {
    const { container } = render(
      <Table>
        <TableBody>
          <TableRow>
            <TableCell>cell</TableCell>
          </TableRow>
        </TableBody>
      </Table>,
    );
    const wrapper = container.querySelector('[data-slot="table-container"]')!;
    expect(wrapper.classList).toContain("@container");
  });
});

// The rule the fix rests on, for every page at once: a table column hides by
// a container breakpoint (`hidden @2xl:table-cell`), never a viewport one.
describe("table columns across the console", () => {
  const VIEWPORT_COLUMN = /(?<![\w@-])(?:sm|md|lg|xl|2xl):table-cell/;
  const sources = import.meta.glob<string>(
    ["/src/**/*.tsx", "!/src/**/*.test.tsx", "!/src/gen/**"],
    { query: "?raw", import: "default", eager: true },
  );

  it("finds the console's sources", () => {
    expect(Object.keys(sources).length).toBeGreaterThan(0);
  });

  it("hides no column by viewport width", () => {
    const offenders = Object.entries(sources)
      .filter(([, src]) => VIEWPORT_COLUMN.test(src))
      .map(([path]) => path);
    expect(offenders).toEqual([]);
  });
});
