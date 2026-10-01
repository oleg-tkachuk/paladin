"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowPathIcon } from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { MCPConnect, MCPOverview } from "@/components/features/mcp/MCPOverview";
import { MCPSessions } from "@/components/features/mcp/MCPSessions";
import {
  MCPAlwaysDeny,
  MCPProfiles,
  MCPTools,
} from "@/components/features/mcp/MCPCatalog";
import { mcpInspectClient } from "@/lib/connect/client";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { errorMessage } from "@/hooks/errorContract";

// /mcp — the MCP server as an operator sees it: is it up, who is connected,
// and what an agent on each transport can do. The configuration (profiles,
// deny list, catalog) comes from one Inspect call; the live view (status,
// sessions) polls on its own.

function Count({ n }: { n: number }) {
  return (
    <Badge variant="secondary" className={cn("ml-1.5", T.labelTight)}>
      {n}
    </Badge>
  );
}

export default function MCPInspectPage() {
  const queryClient = useQueryClient();
  const inspectQuery = useQuery({
    queryKey: ["mcpInspect"],
    queryFn: ({ signal }) => mcpInspectClient.inspect({}, { signal }),
  });
  const inspect = inspectQuery.data ?? null;
  const refreshing = inspectQuery.isFetching;
  const refresh = () => {
    void inspectQuery.refetch();
    void queryClient.invalidateQueries({ queryKey: ["mcpBridgeStatus"] });
    void queryClient.invalidateQueries({ queryKey: ["mcpSessions"] });
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="MCP server"
        description="What agents connected over the Model Context Protocol can see and do."
        showDefaultActions={false}
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={refresh}
            disabled={refreshing}
          >
            <ArrowPathIcon
              className={cn("size-4", refreshing && "animate-spin")}
            />
            Refresh
          </Button>
        }
      />

      <MCPOverview inspect={inspect} />

      {inspectQuery.isPending ? (
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
          reason={errorMessage(inspectQuery.error, "the request failed")}
          onRetry={refresh}
        />
      ) : (
        <>
          <MCPConnect inspect={inspect} />
          <Tabs defaultValue="sessions">
            <TabsList>
              <TabsTrigger value="sessions">Sessions</TabsTrigger>
              <TabsTrigger value="tools">
                Tools <Count n={inspect.toolCatalog.length} />
              </TabsTrigger>
              <TabsTrigger value="profiles">
                Profiles <Count n={inspect.profiles.length} />
              </TabsTrigger>
              <TabsTrigger value="deny">
                Always denied <Count n={inspect.alwaysDeny.length} />
              </TabsTrigger>
            </TabsList>
            <TabsContent value="sessions">
              <MCPSessions />
            </TabsContent>
            <TabsContent value="tools">
              <MCPTools inspect={inspect} />
            </TabsContent>
            <TabsContent value="profiles">
              <MCPProfiles inspect={inspect} />
            </TabsContent>
            <TabsContent value="deny">
              <MCPAlwaysDeny inspect={inspect} />
            </TabsContent>
          </Tabs>
        </>
      )}
    </div>
  );
}
