"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowPathIcon } from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import type { MCPInspectResponse } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";
import { mcpInspectClient } from "@/lib/connect/client";
import { cn } from "@/lib/utils";
import { errorMessage } from "@/hooks/errorContract";

/** The query every MCP page reads its configuration from. */
export const MCP_INSPECT_KEY = ["mcpInspect"] as const;
// The live views poll on their own (mcp-status.ts); Refresh re-reads them too.
const MCP_LIVE_KEYS = [["mcpBridgeStatus"], ["mcpSessions"]] as const;

/** The MCP server's configuration — profiles, catalog, transports, OAuth. */
export function useMcpInspect() {
  return useQuery({
    queryKey: MCP_INSPECT_KEY,
    queryFn: ({ signal }) => mcpInspectClient.inspect({}, { signal }),
  });
}

/**
 * One page of the MCP group: its header with a Refresh that re-reads both the
 * configuration and the live views, and the configuration's loading and
 * failure states, so the four pages say the same thing when Inspect does not
 * answer. `children` renders once the configuration is in.
 */
export function MCPPage({
  title,
  description,
  children,
  before,
}: {
  title: string;
  description: string;
  children: (inspect: MCPInspectResponse) => React.ReactNode;
  /** What renders whether or not the configuration loaded: the live tiles. */
  before?: (inspect: MCPInspectResponse | null) => React.ReactNode;
}) {
  const queryClient = useQueryClient();
  const query = useMcpInspect();
  const inspect = query.data ?? null;
  const refresh = () => {
    void query.refetch();
    for (const key of MCP_LIVE_KEYS) {
      void queryClient.invalidateQueries({ queryKey: key });
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title={title}
        description={description}
        showDefaultActions={false}
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={refresh}
            disabled={query.isFetching}
          >
            <ArrowPathIcon
              className={cn("size-4", query.isFetching && "animate-spin")}
            />
            Refresh
          </Button>
        }
      />
      {before?.(inspect)}
      {query.isPending ? (
        <Card className="space-y-2 p-6">
          <Skeleton className="h-6 w-64" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-5/6" />
        </Card>
      ) : !inspect ? (
        // Without the configuration there are no counts to show: zeros here
        // used to read as "no profiles" and "the deny list was switched off".
        <ListLoadError
          what="the MCP configuration"
          reason={errorMessage(query.error, "the request failed")}
          onRetry={refresh}
        />
      ) : (
        children(inspect)
      )}
    </div>
  );
}
