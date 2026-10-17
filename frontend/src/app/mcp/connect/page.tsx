"use client";

import { MCPConnect } from "@/components/features/mcp/MCPOverview";
import { MCPPage } from "@/components/features/mcp/MCPPage";

// /mcp/connect — how to point an agent at the MCP server: the transports, the
// address and what the token it presents must be.
export default function MCPConnectPage() {
  return (
    <MCPPage
      title="Connect an agent"
      description="The transports the MCP server listens on, and the token an agent presents."
    >
      {(inspect) => <MCPConnect inspect={inspect} />}
    </MCPPage>
  );
}
