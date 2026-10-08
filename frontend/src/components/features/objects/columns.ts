// Visibility of the optional object columns by the table's own width (see
// Table). ObjectsTable's header and ObjectTableRow's cells both read it: a
// header and a row cell that hide at different widths shift every later
// column under the wrong heading.
export const OBJECT_COLUMN_CLASS = {
  object_tag: "hidden @lg:table-cell",
  created: "hidden @3xl:table-cell",
  size: "hidden @4xl:table-cell",
  mime: "hidden @6xl:table-cell",
} as const;
