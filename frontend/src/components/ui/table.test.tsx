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
  // `w-30` is 120px: past that a fixed column crowds a phone-width table.
  const WIDE_FIXED_WIDTH_MIN = 30;
  const isWideFixedWidth = (cls: string) => {
    const m = /^w-(\d+)$/.exec(cls);
    return m !== null && Number(m[1]) >= WIDE_FIXED_WIDTH_MIN;
  };
  const sources = import.meta.glob<string>(
    ["/src/**/*.tsx", "!/src/**/*.test.tsx", "!/src/gen/**"],
    { query: "?raw", import: "default", eager: true },
  );

  it("finds the console's sources", () => {
    expect(Object.keys(sources).length).toBeGreaterThan(0);
  });

  // A phone-width table (343px inside its card) has no room for a column held
  // at 120px or more: an always-visible column takes a wide fixed width only
  // from a container breakpoint (`@md:w-55`), and is sized by content below it.
  it("holds no always-visible column at a wide fixed width on a phone", () => {
    const HEAD =
      /<(?:TableHead|SortableHead)\b[^>]*?className=["`]([^"`]*)["`]/gs;
    const offenders = Object.entries(sources).flatMap(([path, src]) =>
      [...src.matchAll(HEAD)]
        .map((m) => m[1].split(/\s+/))
        .filter((cls) => !cls.includes("hidden"))
        .flatMap((cls) => cls.filter(isWideFixedWidth))
        .map((cls) => `${path}: ${cls}`),
    );
    expect(offenders).toEqual([]);
  });

  it("hides no column by viewport width", () => {
    const offenders = Object.entries(sources)
      .filter(([, src]) => VIEWPORT_COLUMN.test(src))
      .map(([path]) => path);
    expect(offenders).toEqual([]);
  });
});
