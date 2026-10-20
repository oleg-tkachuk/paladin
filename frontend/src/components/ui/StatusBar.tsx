import {
  TooltipContent,
  TooltipRoot,
  TooltipTrigger,
} from "@/components/ui/Tooltip";
import { formatCount } from "@/lib/format/locale";
import { STATUS_ORDER, STATUS_TONE, type StatusTone } from "@/lib/ui/status";
import { cn } from "@/lib/utils";

/** One part of a tally: how many are in a state, and what it is called. */
export type StatusSegment = { tone: StatusTone; label: string; count: number };

/** The segments worth drawing, most urgent first. */
export function statusSegments(segments: StatusSegment[]): StatusSegment[] {
  return segments
    .filter((s) => s.count > 0)
    .sort(
      (a, b) => STATUS_ORDER.indexOf(a.tone) - STATUS_ORDER.indexOf(b.tone),
    );
}

/**
 * A list's items tallied by the one state each leads with, as segments in
 * the order states first appear; statusSegments orders them for the bar.
 */
export function tallyStatus<R>(
  rows: readonly R[],
  statusOf: (row: R) => { label: string; tone: StatusTone },
): StatusSegment[] {
  const byLabel = new Map<string, StatusSegment>();
  for (const row of rows) {
    const { label, tone } = statusOf(row);
    const seen = byLabel.get(label);
    if (seen) seen.count += 1;
    else byLabel.set(label, { tone, label, count: 1 });
  }
  return [...byLabel.values()];
}

/** The tally in words: "2 Error · 1 Maintenance · 9 Healthy". */
export function statusSummary(segments: StatusSegment[]): string {
  return statusSegments(segments)
    .map((s) => `${formatCount(s.count)} ${s.label}`)
    .join(" · ");
}

/**
 * How a set of things splits by state, as one thin bar whose parts are as
 * wide as their share, with the tally in words beside it. After Argo CD's
 * application status bar. A list of one says nothing a bar would add, so it
 * draws nothing; nor does an empty one.
 */
export function StatusBar({
  segments,
  className,
}: {
  segments: StatusSegment[];
  className?: string;
}) {
  const shown = statusSegments(segments);
  const total = shown.reduce((n, s) => n + s.count, 0);
  if (total < 2) return null;
  const summary = statusSummary(shown);
  return (
    <div
      data-slot="status-bar"
      className={cn("flex min-w-0 items-center gap-3", className)}
    >
      <div
        role="img"
        aria-label={summary}
        className="flex h-1.5 min-w-24 flex-1 overflow-hidden rounded-full bg-muted"
      >
        {shown.map((s) => (
          <TooltipRoot key={s.label}>
            <TooltipTrigger asChild>
              <span
                data-tone={s.tone}
                className={cn("h-full", STATUS_TONE[s.tone].fill)}
                style={{ width: `${(s.count / total) * 100}%` }}
              />
            </TooltipTrigger>
            <TooltipContent>{`${formatCount(s.count)} ${s.label}`}</TooltipContent>
          </TooltipRoot>
        ))}
      </div>
      <span
        aria-hidden
        className="shrink-0 text-xs text-muted-foreground tabular-nums"
      >
        {summary}
      </span>
    </div>
  );
}
