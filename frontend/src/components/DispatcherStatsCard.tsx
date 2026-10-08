"use client";

// DispatcherStatsCard — operator view of the event-delivery dispatcher,
// backed by admin/v1.SystemService.GetDispatcherStats. The dispatcher drains
// the transactional outbox (ADR-0003) into event subscriptions; this card
// surfaces the global backlog (pending / failed / oldest) plus the per-
// subscription rows that are actually behind, so a stuck sink is visible
// without grepping metrics. Polls every 15s while mounted.
//
// `available=false` means the stats source isn't reachable (dispatcher not
// running, or this deployment doesn't run one) — we say so plainly rather
// than rendering a misleading all-zeroes rollup.

import { useQuery } from "@tanstack/react-query";

import { adminSystemClient } from "@/lib/connect/client";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { errorMessage } from "@/hooks/errorContract";

function humanizeSeconds(s: number): string {
  if (s <= 0) return "—";
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86_400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86_400)}d`;
}

export function DispatcherStatsCard() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["dispatcherStats"],
    queryFn: () => adminSystemClient.getDispatcherStats({}),
    refetchInterval: 15_000,
    retry: false,
  });

  const behind =
    data?.subscriptions.filter(
      (s) => s.pending > 0n || s.failed > 0n || !!s.lastError,
    ) ?? [];

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-2 pb-3">
        <CardTitle className="text-base">Event dispatcher</CardTitle>
        {data && (
          <Badge
            variant="outline"
            className={data.available ? "text-success" : "text-warning"}
          >
            {data.available ? "live" : "unavailable"}
          </Badge>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        {error ? (
          <p className="text-sm text-destructive">
            {errorMessage(error, "Failed to load dispatcher stats")}
          </p>
        ) : isLoading && !data ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : data && !data.available ? (
          <p className="text-sm text-muted-foreground">
            Dispatcher stats are unavailable in this deployment.
          </p>
        ) : data ? (
          <>
            <div className="grid grid-cols-3 gap-2 text-center">
              <Stat
                label="Pending"
                value={Number(data.pending)}
                accent={data.pending > 0n ? "text-warning" : undefined}
              />
              <Stat
                label="Failed"
                value={Number(data.failed)}
                accent={data.failed > 0n ? "text-destructive" : undefined}
              />
              <Stat
                label="Oldest"
                text={humanizeSeconds(Number(data.oldestPendingSeconds))}
                accent={
                  data.oldestPendingSeconds > 60n ? "text-warning" : undefined
                }
              />
            </div>

            {behind.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                All subscriptions drained.
              </p>
            ) : (
              <div className="space-y-1">
                <p className="text-sm uppercase tracking-wider text-muted-foreground">
                  Subscriptions behind ({behind.length})
                </p>
                <div className="divide-y divide-border/60 overflow-hidden rounded-md border border-border">
                  {behind.map((s) => (
                    <div
                      key={s.subscriptionId}
                      className="flex items-center justify-between gap-2 px-2.5 py-1.5 text-xs"
                    >
                      <div className="min-w-0">
                        <p className={cn(T.code, "truncate")}>
                          {s.subscriptionId}
                        </p>
                        {s.lastError && (
                          <p className="truncate text-sm text-destructive">
                            {s.lastError}
                          </p>
                        )}
                      </div>
                      <div className="flex shrink-0 items-center gap-2">
                        {s.pending > 0n && (
                          <Badge variant="outline" className="text-warning">
                            {Number(s.pending)} pending
                          </Badge>
                        )}
                        {s.failed > 0n && (
                          <Badge variant="outline" className="text-destructive">
                            {Number(s.failed)} failed
                          </Badge>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </>
        ) : null}
      </CardContent>
    </Card>
  );
}

function Stat({
  label,
  value,
  text,
  accent,
}: {
  label: string;
  value?: number;
  text?: string;
  accent?: string;
}) {
  return (
    <div className="rounded-md border border-border bg-muted/30 p-2">
      <p className={cn("text-xl font-semibold tabular-nums", accent)}>
        {text ?? value}
      </p>
      <p className="text-sm uppercase tracking-wider text-muted-foreground">
        {label}
      </p>
    </div>
  );
}
