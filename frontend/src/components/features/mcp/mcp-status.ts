"use client";

import { useQuery } from "@tanstack/react-query";

import { mcpInspectClient } from "@/lib/connect/client";

/** How often the live view (status, sessions) asks again while visible. */
export const MCP_LIVE_POLL_MS = 15_000;

/**
 * The MCP server's live state: whether it answers, each plane's health from
 * its side, and its sessions. Two calls because ListSessions answers an empty
 * list when the server is unreachable — only the status says whether "no
 * sessions" is true or unknown.
 */
export function useMcpLive() {
  const status = useQuery({
    queryKey: ["mcpBridgeStatus"],
    queryFn: ({ signal }) => mcpInspectClient.getBridgeStatus({}, { signal }),
    refetchInterval: MCP_LIVE_POLL_MS,
  });
  const sessions = useQuery({
    queryKey: ["mcpSessions"],
    queryFn: ({ signal }) => mcpInspectClient.listSessions({}, { signal }),
    refetchInterval: MCP_LIVE_POLL_MS,
  });
  return { status, sessions };
}

/** Planes a tool can target, as the catalog names them. */
export const MCP_PLANES = ["admin", "data", "iam"] as const;

export function planeTone(
  plane: string,
): "warning" | "info" | "destructive" | "outline" {
  switch (plane) {
    case "admin":
      return "warning";
    case "data":
      return "info";
    case "iam":
      return "destructive";
    default:
      return "outline";
  }
}
