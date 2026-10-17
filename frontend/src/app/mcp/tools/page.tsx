"use client";

import {
  MCPAlwaysDeny,
  MCPProfiles,
  MCPTools,
} from "@/components/features/mcp/MCPCatalog";
import { MCPPage } from "@/components/features/mcp/MCPPage";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { T } from "@/lib/ui/typography";

// /mcp/tools — what an agent may call: every registered tool, and the
// profiles that choose among them. The always-deny list sits with the
// profiles because it applies after every one of them.
export default function MCPToolsPage() {
  return (
    <MCPPage
      title="Tools"
      description="The tools the MCP server registers, and which ones each profile lets an agent call."
    >
      {(inspect) => (
        <Tabs defaultValue="catalog">
          <TabsList>
            <TabsTrigger value="catalog">Catalog</TabsTrigger>
            <TabsTrigger value="profiles">Profiles</TabsTrigger>
          </TabsList>
          <TabsContent value="catalog">
            <MCPTools inspect={inspect} />
          </TabsContent>
          <TabsContent value="profiles" className="space-y-6">
            <MCPProfiles inspect={inspect} />
            <section className="space-y-3">
              <div className="space-y-1">
                <h2 className={T.cardTitleProse}>Always denied</h2>
                <p className={T.hint}>
                  Removed from every profile, the admin one included.
                </p>
              </div>
              <MCPAlwaysDeny inspect={inspect} />
            </section>
          </TabsContent>
        </Tabs>
      )}
    </MCPPage>
  );
}
