"use client";

import { MCPOverview } from "@/components/features/mcp/MCPOverview";
import { MCPPage } from "@/components/features/mcp/MCPPage";
import { MCPSessions } from "@/components/features/mcp/MCPSessions";
import { T } from "@/lib/ui/typography";

// /mcp — whether the MCP server is up and who is connected to it now. What an
// agent may call (/mcp/tools), how agents authorize (/mcp/access) and how to
// connect one (/mcp/connect) are pages of their own. The tiles and the
// sessions are live and render whether or not the configuration loaded.
export default function MCPOverviewPage() {
  return (
    <MCPPage
      title="MCP server"
      description="Whether the server is up, and the agents connected to it now."
      before={(inspect) => (
        <>
          <MCPOverview inspect={inspect} />
          <section className="space-y-3">
            <h2 className={T.cardTitleProse}>Sessions</h2>
            <MCPSessions />
          </section>
        </>
      )}
    >
      {() => null}
    </MCPPage>
  );
}
