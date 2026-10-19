import { bytesParts } from "@/lib/format/bytes";
import { cn } from "@/lib/utils";

/**
 * A size as a figure and a muted unit, "1.5 KB", with every byte counted in
 * its tooltip. Figures line up in a column: tabular-nums.
 */
export function Bytes({
  value,
  decimals,
  className,
}: {
  value: number | bigint;
  decimals?: number;
  className?: string;
}) {
  const parts = bytesParts(value, decimals);
  return (
    <span title={parts.exact} className={cn("tabular-nums", className)}>
      {parts.value}
      <span className="text-muted-foreground"> {parts.unit}</span>
    </span>
  );
}
