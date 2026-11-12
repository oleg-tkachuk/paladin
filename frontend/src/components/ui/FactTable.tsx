import type { FactMatrix } from "@/lib/health";
import { cn } from "@/lib/utils";

/** What a cell shows where its row reports no such fact. */
export const NO_FACT = "—";

/**
 * Facts as a compact table: a row to a thing, its label first, and a column
 * to a fact. No cell wraps, so every row keeps one line and the columns line
 * up; a table wider than its container scrolls inside it rather than
 * breaking its rows. `columnsMono` sets the column headers as identifiers
 * (role names) rather than words (fact names).
 */
export function FactTable({
  matrix,
  columnsMono = false,
  className,
}: {
  matrix: FactMatrix;
  columnsMono?: boolean;
  className?: string;
}) {
  return (
    <div className={cn("overflow-x-auto", className)}>
      <table className="w-full text-sm">
        <thead>
          <tr className="text-xs text-muted-foreground">
            <th className="pb-1 pr-4 text-left font-normal" />
            {matrix.columns.map((c, i) => (
              <th
                key={c || i}
                scope="col"
                className={cn(
                  "whitespace-nowrap pb-1 pr-4 text-left font-normal last:pr-0",
                  columnsMono && "font-mono",
                )}
              >
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {matrix.rows.map((r) => (
            <tr key={r.label} className="border-t border-border/40">
              <th
                scope="row"
                className="whitespace-nowrap py-1 pr-4 text-left font-mono font-normal"
              >
                {r.label}
              </th>
              {r.cells.map((v, i) => (
                <td
                  key={matrix.columns[i] || i}
                  className={cn(
                    "whitespace-nowrap py-1 pr-4 font-mono tabular-nums last:pr-0",
                    v === undefined && "text-muted-foreground",
                  )}
                >
                  {v ?? NO_FACT}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
