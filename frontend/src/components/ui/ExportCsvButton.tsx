"use client";

// The toolbar's export: the rows the list shows — after its search and
// facets, in its sort, every page of them — saved as a CSV file named after
// the list. What goes in the file is the page's (csvExport); how it is
// written and saved is the same for every list.
import { ArrowDownTrayIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import {
  CSV_BYTE_ORDER_MARK,
  CSV_MIME_TYPE,
  csvFileName,
  type CsvExport,
} from "@/lib/table/csv";
import { saveText } from "@/lib/saveText";

/** Names the button for assistive technology and in its tooltip. */
export const EXPORT_CSV_LABEL = "Export as CSV";

export function ExportCsvButton({ csv }: { csv: CsvExport }) {
  const rows = `${csv.rowCount} ${csv.rowCount === 1 ? "row" : "rows"}`;
  return (
    <Button
      type="button"
      variant="outline"
      size="icon"
      aria-label={EXPORT_CSV_LABEL}
      title={`${EXPORT_CSV_LABEL} (${rows})`}
      disabled={csv.rowCount === 0}
      onClick={() =>
        saveText(
          CSV_BYTE_ORDER_MARK + csv.build(),
          csvFileName(csv.name, new Date()),
          CSV_MIME_TYPE,
        )
      }
    >
      <ArrowDownTrayIcon className="size-4" />
    </Button>
  );
}
