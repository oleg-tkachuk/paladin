"use client";

import { PageHeader } from "@/components/layout/PageHeader";
import { ConnectedApplications } from "@/components/features/oauth/ConnectedApplications";
import { useAuth } from "@/context/AuthContext";
import { userResourceName } from "@/lib/userPath";

/**
 * The applications the signed-in user has let in through OAuth (ADR-0035):
 * agents and tools holding a grant to act as them. A page of its own rather
 * than a card at the foot of the profile: it is access to manage, read when
 * something is acting on the user's behalf, not a setting of the account.
 */
export default function ConnectedApplicationsPage() {
  const { user } = useAuth();
  return (
    <div className="space-y-4">
      <PageHeader
        title="Connected applications"
        description="Agents and tools you let act as you through OAuth, such as one connected to the MCP server. Revoking one ends its access."
        showDefaultActions={false}
      />
      {user ? (
        <ConnectedApplications
          userName={userResourceName(user.tenantId, user.userId)}
          own
        />
      ) : null}
    </div>
  );
}
