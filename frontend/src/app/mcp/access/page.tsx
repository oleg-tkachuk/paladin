"use client";

import { MCPAuthorization } from "@/components/features/mcp/MCPAuthorization";
import { MCPPage } from "@/components/features/mcp/MCPPage";

// /mcp/access — how agents authorize: the OAuth posture of the MCP server and
// the Authorization Server, and the clients registered with it. A user's own
// grants are on their profile.
export default function MCPAccessPage() {
  return (
    <MCPPage
      title="Access"
      description="How agents authorize to the MCP server, and the OAuth clients that may."
    >
      {(inspect) => <MCPAuthorization inspect={inspect} />}
    </MCPPage>
  );
}
