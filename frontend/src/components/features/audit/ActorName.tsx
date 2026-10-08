"use client";

import { useQuery } from "@tanstack/react-query";

import { userClient } from "@/lib/connect/client";
import { notFoundIsAnswer } from "@/lib/connect/expected";
import { isUuid } from "@/lib/resources/tenant-resolve";

/** How long a resolved name is reused; users are renamed rarely. */
export const ACTOR_NAME_STALE_MS = 5 * 60_000;

/**
 * The actor of an audit entry, by name.
 *
 * The audit log records the token's subject, which for a person is their user
 * id, so every row read as a UUID. A user id is looked up once per session and
 * shown as the display name, with the id kept in the tooltip. Anything else —
 * "system:bootstrap", a lookup the caller may not make, a user deleted since —
 * is shown as recorded, because the recorded value is still the truth.
 */
export function ActorName({
  subject,
  tenantId,
  fallback = "—",
}: {
  subject: string;
  tenantId: string;
  fallback?: string;
}) {
  const resolvable = isUuid(subject) && isUuid(tenantId);
  const user = useQuery({
    queryKey: ["actorName", tenantId, subject],
    queryFn: ({ signal }) =>
      userClient.getUser(
        { name: `tenants/${tenantId}/users/${subject}` },
        // A user deleted since is shown as recorded, not reported.
        notFoundIsAnswer({ signal }),
      ),
    enabled: resolvable,
    staleTime: ACTOR_NAME_STALE_MS,
    retry: false,
  });

  if (!subject) return <>{fallback}</>;
  const name = user.data?.displayName || user.data?.subject;
  return <span title={subject}>{name || subject}</span>;
}
