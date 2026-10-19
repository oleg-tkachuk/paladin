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

/** The object list's columns, each of which the user may hide. */
export const COLUMN_OPTIONS = [
  { id: "key", label: "Name / ID" },
  { id: "object_tag", label: "ObjectTag" },
  { id: "mime", label: "MIME Type" },
  { id: "size", label: "Size" },
  { id: "status", label: "Sync Status" },
  { id: "created", label: "Initialization" },
  { id: "actions", label: "Operations" },
];
