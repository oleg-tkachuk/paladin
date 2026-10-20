// One way a list's items split, for its summary view: each value with how
// many items have it, written as a legend reads, "aws (3)". A value that is
// a state carries its tone's dot, beside the words that name it.
import type React from "react";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { statusSegments, type StatusSegment } from "@/components/ui/StatusBar";
import { StatusDot } from "@/components/ui/StatusIcon";
import { formatCount } from "@/lib/format/locale";
import type { StatusTone } from "@/lib/ui/status";

export interface BreakdownEntry {
  key: string;
  count: number;
  tone?: StatusTone;
}

/** A status tally as entries, what needs attention first. */
export function stateEntries(segments: StatusSegment[]): BreakdownEntry[] {
  return statusSegments(segments).map((s) => ({
    key: s.label,
    count: s.count,
    tone: s.tone,
  }));
}

export function Breakdown({
  title,
  icon,
  entries,
}: {
  title: string;
  icon: React.ElementType;
  entries: readonly BreakdownEntry[];
}) {
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle icon={icon}>{title}</CardTitle>
      </CardHeader>
      <CardContent>
        <ul className="space-y-1.5">
          {entries.map((e) => (
            <li key={e.key} className="flex items-center gap-2 text-sm">
              {e.tone && <StatusDot tone={e.tone} />}
              <span className="min-w-0 break-words">
                {e.key}{" "}
                <span className="text-muted-foreground tabular-nums">
                  ({formatCount(e.count)})
                </span>
              </span>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  );
}
