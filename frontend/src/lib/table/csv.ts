// A list as CSV: the columns a table renders, the rows it shows, in its order.
// RFC 4180 text — fields joined by commas, records ended by CRLF, a field
// holding a comma, a quote or a line break quoted with its quotes doubled —
// with one departure a spreadsheet makes necessary: a cell that would open as
// a formula is written as text. Pure, so every list exports the same way and
// the rules are tested once.

/** One column of an export: the header the table shows and a row's text. */
export interface CsvColumn<R> {
  header: string;
  text: (row: R) => string;
}

/** The media type of an export, as the download names it. */
export const CSV_MIME_TYPE = "text/csv;charset=utf-8";

/** The file name ending of an export. */
export const CSV_EXTENSION = ".csv";

/**
 * Leads the file, so a spreadsheet reads it as UTF-8 rather than guessing a
 * legacy code page and garbling every name outside ASCII.
 */
export const CSV_BYTE_ORDER_MARK = "﻿";

const FIELD_SEPARATOR = ",";
const RECORD_SEPARATOR = "\r\n";
const QUOTE = '"';
const ESCAPED_QUOTE = '""';

/**
 * The first characters a spreadsheet reads as the start of a formula, or as
 * a way to hide one (a leading tab or carriage return), after OWASP's CSV
 * injection guidance.
 */
const FORMULA_LEADS: readonly string[] = ["=", "+", "-", "@", "\t", "\r"];

/** Put before a cell that would open as a formula, so it opens as text. */
const FORMULA_GUARD = "'";

/** Characters that make a field need quotes. */
const NEEDS_QUOTES = /[",\r\n]/;

/** The text as a spreadsheet will show it, never as a formula it runs. */
export function neutraliseFormula(text: string): string {
  return FORMULA_LEADS.some((lead) => text.startsWith(lead))
    ? `${FORMULA_GUARD}${text}`
    : text;
}

/** One field: neutralised first, then quoted when RFC 4180 needs it. */
export function csvField(text: string): string {
  const safe = neutraliseFormula(text);
  return NEEDS_QUOTES.test(safe)
    ? `${QUOTE}${safe.replaceAll(QUOTE, ESCAPED_QUOTE)}${QUOTE}`
    : safe;
}

function record(fields: readonly string[]): string {
  return fields.map(csvField).join(FIELD_SEPARATOR) + RECORD_SEPARATOR;
}

/** The header record, then one record per row, in the order given. */
export function toCsv<R>(
  columns: readonly CsvColumn<R>[],
  rows: readonly R[],
): string {
  return (
    record(columns.map((c) => c.header)) +
    rows.map((row) => record(columns.map((c) => c.text(row)))).join("")
  );
}

/** Pads a date part to two digits. */
const TWO_DIGITS = 2;
const pad = (n: number) => String(n).padStart(TWO_DIGITS, "0");

/** Anything but letters, digits and dashes in a list's name. */
const UNSAFE_IN_FILE_NAME = /[^a-z0-9-]+/g;
/** Dashes left at either end once the unsafe characters became dashes. */
const EDGE_DASHES = /^-+|-+$/g;
/** Stands for a list whose name leaves nothing a file name may hold. */
const UNNAMED_LIST = "export";

/** Joins the parts of a list's name, and the name to its date in a file. */
const NAME_SEPARATOR = "-";

/**
 * The name of a list within one resource — a tenant's buckets — so its
 * export says whose rows it holds: acme-buckets.
 */
export function scopedListName(scope: string, list: string): string {
  return `${scope}${NAME_SEPARATOR}${list}`;
}

/**
 * The file an export downloads as: the list's name and the local time it
 * was taken, as in users-20261124-153012.csv, so two exports sort in the
 * order they were made and neither overwrites the other.
 */
export function csvFileName(name: string, at: Date): string {
  const slug =
    name
      .toLowerCase()
      .replace(UNSAFE_IN_FILE_NAME, NAME_SEPARATOR)
      .replace(EDGE_DASHES, "") || UNNAMED_LIST;
  const date = `${at.getFullYear()}${pad(at.getMonth() + 1)}${pad(at.getDate())}`;
  const time = `${pad(at.getHours())}${pad(at.getMinutes())}${pad(at.getSeconds())}`;
  return [slug, date, time].join(NAME_SEPARATOR) + CSV_EXTENSION;
}

/**
 * What a toolbar's export button takes: the list's name, how many rows it
 * would write, and the text, built only when asked for.
 */
export interface CsvExport {
  name: string;
  rowCount: number;
  build: () => string;
}

/** An export of `rows` through `columns`, as the toolbar takes it. */
export function csvExport<R>(
  name: string,
  columns: readonly CsvColumn<R>[],
  rows: readonly R[],
): CsvExport {
  return { name, rowCount: rows.length, build: () => toCsv(columns, rows) };
}
