"use client";

import {
  DENIED_TABLE,
  MCPAlwaysDeny,
  MCPProfiles,
  MCPTools,
  PROFILES_TABLE,
} from "@/components/features/mcp/MCPCatalog";
import { MCPPage } from "@/components/features/mcp/MCPPage";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useUrlState, type UrlCodec } from "@/hooks/useUrlState";
import { tableParam } from "@/lib/table/urlState";
import { T } from "@/lib/ui/typography";

/** The page's tabs. */
const TAB = { catalog: "catalog", profiles: "profiles" } as const;
type Tab = (typeof TAB)[keyof typeof TAB];

// The tables on the Profiles tab: a link to a record of one opens that tab.
const PROFILE_TAB_TABLES = [PROFILES_TABLE, DENIED_TABLE];

/** The tab a link lands on; read only, since each table writes its own id. */
const LINKED_TAB: UrlCodec<Tab> = {
  read: (params) =>
    PROFILE_TAB_TABLES.some((url) => params.get(tableParam("row", url)))
      ? TAB.profiles
      : TAB.catalog,
  write: () => ({}),
};

// /mcp/tools — what an agent may call: every registered tool, and the
// profiles that choose among them. The always-deny list sits with the
// profiles because it applies after every one of them.
export default function MCPToolsPage() {
  const [linkedTab] = useUrlState(LINKED_TAB, TAB.catalog);
  return (
    <MCPPage
      title="Tools"
      description="The tools the MCP server registers, and which ones each profile lets an agent call."
    >
      {(inspect) => (
        <Tabs defaultValue={linkedTab}>
          <TabsList>
            <TabsTrigger value={TAB.catalog}>Catalog</TabsTrigger>
            <TabsTrigger value={TAB.profiles}>Profiles</TabsTrigger>
          </TabsList>
          <TabsContent value={TAB.catalog}>
            <MCPTools inspect={inspect} />
          </TabsContent>
          <TabsContent value={TAB.profiles} className="space-y-6">
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
