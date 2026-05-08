"use client";

import { useCallback, useEffect, useState } from "react";
import { ConnectError } from "@connectrpc/connect";

import { auditClient } from "@/lib/connect/client";
import type { AuditLogEntry } from "@/gen/paladin/admin/v1/types_pb";
import { API_PAGE_SIZE_MAX } from "@/constants";

// useAuditLogs — wrapper around admin/v1.AuditLogService.ListAuditLog.
//
// Returns the latest entries for the active tenant. Pagination uses the
// server-issued page_token; loadMore appends, refresh resets to page 1.
// Filter is a CEL expression accepted as-is by the backend (see proto
// comment on ListAuditLogRequest.filter).

export type { AuditLogEntry };

export interface UseAuditLogsResult {
  entries: AuditLogEntry[];
  loading: boolean;
  error: string | null;
  nextCursor: string;
  refresh: () => Promise<void>;
  loadMore: () => Promise<void>;
}

export function useAuditLogs(
  pageSize: number = 50,
  filter: string = "",
): UseAuditLogsResult {
  const [entries, setEntries] = useState<AuditLogEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [nextCursor, setNextCursor] = useState<string>("");

  const fetchPage = useCallback(
    async (pageToken: string, append: boolean) => {
      setLoading(true);
      setError(null);
      try {
        const res = await auditClient.listAuditLog({
          page: {
            pageSize: Math.min(pageSize, API_PAGE_SIZE_MAX),
            pageToken,
          },
          filter,
        });
        setEntries((prev) =>
          append ? [...prev, ...res.entries] : res.entries,
        );
        setNextCursor(res.page?.nextPageToken ?? "");
      } catch (err) {
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to load audit log";
        setError(msg);
      } finally {
        setLoading(false);
      }
    },
    [pageSize, filter],
  );

  const refresh = useCallback(() => fetchPage("", false), [fetchPage]);
  const loadMore = useCallback(
    () => (nextCursor ? fetchPage(nextCursor, true) : Promise.resolve()),
    [nextCursor, fetchPage],
  );

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { entries, loading, error, nextCursor, refresh, loadMore };
}
