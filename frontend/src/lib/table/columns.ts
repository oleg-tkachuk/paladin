// A list's columns, declared once: the header its table renders and the text
// each row holds in it. The header row reads its labels from here, and an
// export reads the same headers and the same text, so the file says what the
// table says. A column with no text — a row's actions — is rendered and not
// exported.
import type { CsvColumn } from "./csv";

/** One column of a list. */
export interface TableColumn<R> {
  /** The column's header, as the table shows it and an export names it. */
  header: string;
  /**
   * A row's value in this column as plain text: the full value where the
   * cell shortens it, an absolute time where the cell says "3 hours ago".
   */
  text?: (row: R) => string;
}

/** A list's columns by id, in the order the table renders them. */
export type TableColumns<R, K extends string = string> = Readonly<
  Record<K, TableColumn<R>>
>;

/**
 * The columns an export writes: each column holding text, in the table's
 * order, less the ones the user hid.
 */
export function exportColumns<R>(
  columns: TableColumns<R>,
  hidden: readonly string[] = [],
): CsvColumn<R>[] {
  return Object.entries(columns).flatMap(([id, c]) =>
    c.text && !hidden.includes(id) ? [{ header: c.header, text: c.text }] : [],
  );
}
