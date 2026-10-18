import { Badge } from "@/components/ui/badge";
import { LIFECYCLE_BADGE, type Lifecycle } from "@/lib/table/lifecycle";
import { cn } from "@/lib/utils";

/** A row's lifecycle state, named and coloured the same in every table. */
export function LifecycleBadge({
  state,
  className,
}: {
  state: Lifecycle;
  className?: string;
}) {
  const b = LIFECYCLE_BADGE[state];
  return (
    <Badge variant={b.variant} className={cn(className)}>
      {b.label}
    </Badge>
  );
}
