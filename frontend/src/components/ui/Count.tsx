import { formatCompactCount, formatCount } from "@/lib/format/locale";
import { cn } from "@/lib/utils";

/**
 * A count for a tight space: "12.3k", with the exact "12,345" on hover. A
 * count small enough to show whole has no tooltip, since it hides nothing.
 */
export function Count({
  value,
  className,
}: {
  value: number | bigint;
  className?: string;
}) {
  const short = formatCompactCount(value);
  const exact = formatCount(value);
  return (
    <span
      title={short === exact ? undefined : exact}
      className={cn("tabular-nums", className)}
    >
      {short}
    </span>
  );
}
