"use client";

import { useState } from "react";

import { DetailPanel } from "@/components/ui/DetailPanel";
import {
  RecordField,
  RecordFields,
  openableRow,
} from "@/components/ui/RecordDialog";
import { Timestamp } from "@/components/Timestamp";
import type { MCPSession } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";

import { Card } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { RelativeTime } from "@/components/RelativeTime";
import { SortableHead } from "@/components/ui/SortHeader";
import { TableToolbar } from "@/components/ui/TableToolbar";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useDetailPanel } from "@/hooks/useDetailPanel";
import { useTableView } from "@/hooks/useTableView";
import type { TableUrl } from "@/lib/table/urlState";
import type { SortAccessors } from "@/lib/table/view";
import { T } from "@/lib/ui/typography";

import { useMcpLive } from "./mcp-status";
import { errorMessage } from "@/hooks/errorContract";
import { Duration, UNTIL_NOW } from "@/components/Duration";

// The session list keeps its search, sort and open session in the URL under
// its own names, beside the page's other tables.
const SESSIONS_TABLE: TableUrl = { namespace: "sessions" };

/** Characters of a session id shown; enough to tell rows apart. */
const SESSION_ID_SHOWN = 8;

// The columns of the sessions table; the no-match row spans them.
const SESSION_COLUMNS = 6;

type SessionColumn =
  "session" | "agent" | "started" | "seen" | "calls" | "requests";
const at = (t: MCPSession["startedAt"]) => (t ? timestampDate(t) : undefined);
const SESSION_SORT: SortAccessors<MCPSession, SessionColumn> = {
  session: (s) => s.id,
  agent: (s) => s.agentSubject || undefined,
  started: (s) => at(s.startedAt),
  seen: (s) => at(s.lastSeen),
  calls: (s) => s.toolCallCount,
  requests: (s) => s.requestCount,
};
const SESSION_SEARCH = (s: MCPSession) => [s.id, s.agentSubject];

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
  const all = sessions.data?.sessions ?? [];
  // The open session is in the URL. The row clicked stays at hand for a
  // session the live list has since dropped.
  const detail = useDetailPanel(undefined, SESSIONS_TABLE);
  const [clicked, setClicked] = useState<MCPSession | null>(null);
  const opened = detail.openId
    ? (all.find((s) => s.id === detail.openId) ??
      (clicked?.id === detail.openId ? clicked : null))
    : null;
  const view = useTableView(all, {
    text: SESSION_SEARCH,
    accessors: SESSION_SORT,
    url: SESSIONS_TABLE,
  });
  const sortProps = { current: view.sort, onSort: view.toggleSort };

  if (sessions.isPending || status.isPending) {
    return (
      <Card className="space-y-2 p-4">
        <Skeleton className="h-5 w-full" />
        <Skeleton className="h-5 w-5/6" />
      </Card>
    );
  }
  if (all.length === 0) {
    return (
      <Card className="p-8 text-center">
        <p className={unreachable ? "text-sm text-warning" : T.helper}>
          {unreachable
            ? `The MCP server could not be reached, so whether anyone is connected is unknown: ${unreachable}`
            : "No agent is connected. A session appears here when one initializes over streamable HTTP."}
        </p>
      </Card>
    );
  }
  return (
    <div className="space-y-3">
      <TableToolbar
        query={view.query}
        onQueryChange={view.setQuery}
        searchLabel="Search sessions"
        placeholder="Search by session or agent…"
        count={{ shown: view.rows.length, total: view.total }}
      />
      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <SortableHead label="Session" column="session" {...sortProps} />
              <SortableHead label="Agent" column="agent" {...sortProps} />
              <SortableHead label="Started" column="started" {...sortProps} />
              <SortableHead label="Last seen" column="seen" {...sortProps} />
              <SortableHead
                label="Tool calls"
                column="calls"
                align="end"
                className="text-right"
                {...sortProps}
              />
              <SortableHead
                label="Requests"
                column="requests"
                align="end"
                className="text-right"
                {...sortProps}
              />
            </TableRow>
          </TableHeader>
          <TableBody>
            {view.rows.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={SESSION_COLUMNS}
                  className="py-8 text-center text-xs text-muted-foreground"
                >
                  No session matches the search.
                </TableCell>
              </TableRow>
            ) : null}
            {view.rows.map((s) => (
              <TableRow
                key={s.id}
                {...openableRow(() => {
                  setClicked(s);
                  detail.open(s.id);
                })}
                {...detail.rowProps(s.id)}
              >
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
        <SessionPanel session={opened} onClose={detail.close} />
      </Card>
    </div>
  );
}

/** One session's record, in a panel beside the list. */
function SessionPanel({
  session,
  onClose,
}: {
  session: MCPSession | null;
  onClose: () => void;
}) {
  return (
    <DetailPanel
      open={session !== null}
      onClose={onClose}
      rowId={session?.id}
      title="Session"
      identifier={session?.id}
    >
      {session ? (
        <RecordFields>
          <RecordField label="Agent" mono>
            {session.agentSubject || "unknown"}
          </RecordField>
          <RecordField label="Started">
            <Timestamp ts={session.startedAt} />
          </RecordField>
          <RecordField label="Last seen">
            <Timestamp ts={session.lastSeen} />
          </RecordField>
          <RecordField label="Connected for">
            <Duration from={session.startedAt} to={UNTIL_NOW} />
          </RecordField>
          <RecordField label="Tool calls">
            {session.toolCallCount.toString()}
          </RecordField>
          <RecordField label="Requests">
            {session.requestCount.toString()}
          </RecordField>
        </RecordFields>
      ) : null}
    </DetailPanel>
  );
}
