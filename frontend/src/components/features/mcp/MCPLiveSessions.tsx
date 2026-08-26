"use client";

import { useEffect, useState } from "react";

import Link from "next/link";

import { ConnectError } from "@connectrpc/connect";

import type { MCPSession } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";
import { mcpInspectClient } from "@/lib/connect/client";

// Protobuf Timestamp → local string. Manual (seconds/nanos) to avoid a wkt
// import; nanos precision is irrelevant for a "last seen" column.
function fmtTs(ts: { seconds: bigint; nanos: number } | undefined): string {
  if (!ts || ts.seconds === 0n) return "—";
  return new Date(Number(ts.seconds) * 1000).toLocaleString();
}

/**
 * MCPLiveSessions renders the live streamable-HTTP sessions from
 * MCPInspectService.ListSessions (which proxies the MCP server's registry).
 *
 * ListSessions answers an empty list when the bridge is unreachable — a
 * deliberate degrade, so one dead replica cannot fail the call, but one that
 * made "the bridge is down" and "nobody is using MCP" render identically. So
 * the empty state asks GetBridgeStatus which of the two it is, and says so.
 * An operator opens this page when something looks wrong; a tidy "no active
 * sessions" over an unreachable bridge is the worst thing it could show.
 */
export function MCPLiveSessions() {
  const [sessions, setSessions] = useState<MCPSession[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [bridgeError, setBridgeError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    // Both in one pass: the sessions, and whether the bridge could be asked at
    // all. Two calls rather than one because they answer different questions —
    // and the second is the one that makes an empty first honest.
    Promise.allSettled([
      mcpInspectClient.listSessions({}),
      mcpInspectClient.getBridgeStatus({}),
    ])
      .then(([list, status]) => {
        if (cancelled) return;
        setSessions(list.status === "fulfilled" ? list.value.sessions : []);
        if (status.status === "fulfilled") {
          setBridgeError(
            status.value.reachable
              ? null
              : status.value.error || "the MCP server could not be reached",
          );
        } else {
          setBridgeError(ConnectError.from(status.reason).message);
        }
      })
      .finally(() => {
        if (!cancelled) setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className="rounded-2xl border border-white/5 bg-surface/30 p-4">
      <div className="mb-3 flex items-center gap-2">
        <h3 className="text-sm font-medium text-white">Live sessions</h3>
        <span className="rounded-md border border-white/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-slate-400">
          {loaded ? sessions.length : "…"}
        </span>
      </div>

      {loaded && sessions.length === 0 && bridgeError ? (
        <p className="text-xs text-amber-400">
          The MCP server could not be reached, so this list is not empty — it is
          unknown. {bridgeError}
        </p>
      ) : loaded && sessions.length === 0 ? (
        <p className="text-xs text-slate-400">
          No active sessions on this MCP server. MCP tool calls are also visible
          in the audit log — filter by{" "}
          <Link
            href="/audit?audience=paladin-mcp"
            className="text-indigo-400 hover:underline"
          >
            audience=paladin-mcp
          </Link>
          .
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="text-slate-500">
              <tr className="border-b border-white/5">
                <th className="py-2 pr-4 font-semibold">Session</th>
                <th className="py-2 pr-4 font-semibold">Agent</th>
                <th className="py-2 pr-4 font-semibold">Started</th>
                <th className="py-2 pr-4 font-semibold">Last seen</th>
                <th className="py-2 pr-4 text-right font-semibold">
                  Tool calls
                </th>
                <th className="py-2 text-right font-semibold">Requests</th>
              </tr>
            </thead>
            <tbody className="text-slate-300">
              {sessions.map((s) => (
                <tr
                  key={s.id}
                  className="border-b border-white/5 last:border-0"
                >
                  <td className="py-2 pr-4 font-mono text-[11px]">{s.id}</td>
                  <td className="py-2 pr-4">{s.agentSubject || "—"}</td>
                  <td className="py-2 pr-4">{fmtTs(s.startedAt)}</td>
                  <td className="py-2 pr-4">{fmtTs(s.lastSeen)}</td>
                  <td className="py-2 pr-4 text-right tabular-nums">
                    {s.toolCallCount.toString()}
                  </td>
                  <td className="py-2 text-right tabular-nums">
                    {s.requestCount.toString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
