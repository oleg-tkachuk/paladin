// Gives each body cell of a table the name of its column, as data-label, so
// a narrow screen can set a row out as a card of "label: value" pairs while
// the header row is hidden. Read off the header the page already renders, so
// no page has to name its columns twice.

/** What a header cell is called: its accessible label, else its text. */
function headLabel(th: HTMLTableCellElement): string {
  return (th.getAttribute("aria-label") ?? th.textContent ?? "").trim();
}

export function labelCells(table: HTMLTableElement): void {
  // One entry per column: a header spanning two names both.
  const heads = Array.from(table.tHead?.rows[0]?.cells ?? []).flatMap((th) =>
    Array<string>(th.colSpan).fill(headLabel(th)),
  );
  for (const body of Array.from(table.tBodies)) {
    for (const row of Array.from(body.rows)) {
      // A row of one cell across the table — an empty state, a skeleton —
      // is not a record and takes no labels.
      if (row.cells.length === 1 && row.cells[0].colSpan > 1) continue;
      let column = 0;
      for (const cell of Array.from(row.cells)) {
        const label = heads[column] ?? "";
        if (label) cell.dataset.label = label;
        else delete cell.dataset.label;
        column += cell.colSpan;
      }
    }
  }
}
