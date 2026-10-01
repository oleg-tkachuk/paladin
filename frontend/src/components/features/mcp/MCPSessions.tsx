"use client";

import { Card } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { RelativeTime } from "@/components/RelativeTime";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { T } from "@/lib/ui/typography";

import { useMcpLive } from "./mcp-status";
import { errorMessage } from "@/hooks/errorContract";

/** Characters of a session id shown; enough to tell rows apart. */
const SESSION_ID_SHOWN = 8;

/**
 * Live streamable-HTTP sessions, refreshed while the page is open.
 *
 * ListSessions answers an empty list when the server is unreachable — a
 * deliberate degrade, so one dead replica cannot fail the call — which made
 * "the server is down" and "nobody is connected" look the same. The status
 * call decides which one an empty list is.
 */
export function MCPSessions() {
  const { status, sessions } = useMcpLive();
  const unreachable = status.error
    ? errorMessage(status.error)
    : status.data && !status.data.reachable
      ? status.data.error || "the MCP server could not be reached"
      : null;
  const rows = sessions.data?.sessions ?? [];

  if (sessions.isPending || status.isPending) {
    return (
      <Card className="space-y-2 p-4">
        <Skeleton className="h-5 w-full" />
        <Skeleton className="h-5 w-5/6" />
      </Card>
    );
  }
  if (rows.length === 0) {
    return (
      <Card className="p-8 text-center">
        <p className={unreachable ? "text-sm text-amber-500" : T.helper}>
          {unreachable
            ? `The MCP server could not be reached, so whether anyone is connected is unknown: ${unreachable}`
            : "No agent is connected. A session appears here when one initializes over streamable HTTP."}
        </p>
      </Card>
    );
  }
  return (
    <Card className="p-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Session</TableHead>
            <TableHead>Agent</TableHead>
            <TableHead>Started</TableHead>
            <TableHead>Last seen</TableHead>
            <TableHead className="text-right">Tool calls</TableHead>
            <TableHead className="text-right">Requests</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((s) => (
            <TableRow key={s.id}>
              <TableCell className={T.code} title={s.id}>
                {s.id.slice(0, SESSION_ID_SHOWN)}…
              </TableCell>
              <TableCell>
                {s.agentSubject || (
                  <span className="text-muted-foreground">unknown</span>
                )}
              </TableCell>
              <TableCell>
                <RelativeTime ts={s.startedAt} />
              </TableCell>
              <TableCell>
                <RelativeTime ts={s.lastSeen} />
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {s.toolCallCount.toString()}
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {s.requestCount.toString()}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}
