import type React from "react";

import { Badge } from "@/components/ui/badge";
import type { StatusTone } from "@/lib/ui/status";

/** The badge variant that carries each tone: its own, but danger's is named destructive. */
export const STATUS_BADGE_VARIANT = {
  success: "success",
  progress: "progress",
  warning: "warning",
  danger: "destructive",
  suspended: "suspended",
  unknown: "unknown",
} as const satisfies Record<StatusTone, string>;

/** A state named in words, as a chip in its tone, for a dense table row. */
export function StatusBadge({
  tone,
  children,
  className,
}: {
  tone: StatusTone;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <Badge
      data-tone={tone}
      variant={STATUS_BADGE_VARIANT[tone]}
      className={className}
    >
      {children}
    </Badge>
  );
}
