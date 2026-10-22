import { Card } from "@/components/ui/Card";
import { T } from "@/lib/ui/typography";

/** The height a KPI tile takes, so its placeholder keeps the same place. */
export const KPI_TILE_HEIGHT = "h-18";

/**
 * A headline figure: its label, with the tile's one action beside it, over
 * the value and what qualifies it on one line. The card's own gap and
 * padding are tightened so a row of these stays a band, not a block.
 */
export function KPITile({
  label,
  value,
  helper,
  action,
  footer,
}: {
  label: string;
  value: React.ReactNode;
  helper?: React.ReactNode;
  action?: React.ReactNode;
  footer?: React.ReactNode;
}) {
  return (
    <Card className="gap-1.5 px-4 py-3" aria-label={label} role="group">
      <div className="flex items-baseline justify-between gap-2">
        <h3 className={T.label}>{label}</h3>
        {action}
      </div>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <span className="text-xl font-semibold leading-tight tabular-nums">
          {value}
        </span>
        {helper && <span className={T.hint}>{helper}</span>}
      </div>
      {footer}
    </Card>
  );
}
