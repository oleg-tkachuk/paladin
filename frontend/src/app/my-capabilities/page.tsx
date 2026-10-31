"use client";

import { PageHeader } from "@/components/layout/PageHeader";
import { UserCapabilities } from "@/components/features/oauth/UserCapabilities";
import { useAuth } from "@/context/AuthContext";
import { userResourceName } from "@/lib/userPath";

/**
 * What the agents the signed-in user let in may do as them: each capability
 * one exchanged under a grant of theirs. Beside Connected applications rather
 * than in it: a grant says who may act as the user, a capability what it may
 * do and until when, and a busy agent holds many.
 */
export default function MyCapabilitiesPage() {
  const { user } = useAuth();
  return (
    <div className="space-y-4">
      <PageHeader
        title="My capabilities"
        description="What the applications you connected may do as you, and until when. Revoking an application under Connected applications revokes its capabilities."
        showDefaultActions={false}
      />
      {user ? (
        <UserCapabilities
          userName={userResourceName(user.tenantId, user.userId)}
          own
        />
      ) : null}
    </div>
  );
}
