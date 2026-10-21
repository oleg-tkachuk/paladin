"use client";

import { useQuery } from "@tanstack/react-query";

import {
  apiTokenClient,
  capabilityClient,
  userClient,
} from "@/lib/connect/client";
import { notFoundIsAnswer } from "@/lib/connect/expected";
import { parseActor } from "@/lib/actor";
import { isUuid } from "@/lib/resources/tenant-resolve";
import {
  CapabilityHoverCard,
  UserHoverCard,
} from "@/components/features/hover/ResourceHoverCards";

/** How long a resolved name is reused; actors are renamed rarely. */
export const ACTOR_NAME_STALE_MS = 5 * 60_000;

/** What an API token's name is shown with, so it does not read as a person. */
export const API_TOKEN_LABEL = "token";

/**
 * The actor of an audit entry, by name.
 *
 * The audit log records the token's subject: a person's user id, an API
 * token's `apikey:<id>`, a capability's `capability:<id>`, so every row read
 * as an id. Each is looked up once per session — the user's display name, the
 * API token's, the subject the capability was issued to — with the recorded
 * subject kept in the tooltip. Anything else — "system:bootstrap", a lookup
 * the caller may not make, an actor deleted since — is shown as recorded,
 * because the recorded value is still the truth.
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
  const actor = parseActor(subject);
  const inTenant = isUuid(tenantId);
  const query = {
    staleTime: ACTOR_NAME_STALE_MS,
    retry: false,
  } as const;
  const user = useQuery({
    queryKey: ["actorName", "user", tenantId, subject],
    queryFn: ({ signal }) =>
      userClient.getUser(
        { name: `tenants/${tenantId}/users/${subject}` },
        // An actor deleted since is shown as recorded, not reported.
        notFoundIsAnswer({ signal }),
      ),
    enabled: actor.kind === "user" && inTenant,
    ...query,
  });
  const token = useQuery({
    queryKey: ["actorName", "apiToken", tenantId, subject],
    queryFn: ({ signal }) =>
      apiTokenClient.get(
        {
          name: `tenants/${tenantId}/apiTokens/${actor.kind === "apiToken" ? actor.id : ""}`,
        },
        notFoundIsAnswer({ signal }),
      ),
    enabled: actor.kind === "apiToken" && inTenant,
    ...query,
  });
  const capability = useQuery({
    queryKey: ["actorName", "capability", subject],
    queryFn: ({ signal }) =>
      capabilityClient.get(
        { id: actor.kind === "capability" ? actor.id : "" },
        notFoundIsAnswer({ signal }),
      ),
    enabled: actor.kind === "capability",
    ...query,
  });

  if (!subject) return <>{fallback}</>;
  switch (actor.kind) {
    case "user": {
      const name = user.data?.displayName || user.data?.subject;
      const label = <span title={subject}>{name || subject}</span>;
      return inTenant ? (
        <UserHoverCard name={`tenants/${tenantId}/users/${subject}`}>
          {label}
        </UserHoverCard>
      ) : (
        label
      );
    }
    case "apiToken": {
      const name = token.data?.apiToken?.displayName;
      return (
        <span title={subject}>
          {name ? (
            <>
              {name}{" "}
              <span className="text-muted-foreground">{API_TOKEN_LABEL}</span>
            </>
          ) : (
            subject
          )}
        </span>
      );
    }
    case "capability": {
      const name = capability.data?.capability?.subject?.subject;
      const label = <span title={subject}>{name || subject}</span>;
      return inTenant ? (
        <CapabilityHoverCard id={actor.id} tenantId={tenantId}>
          {label}
        </CapabilityHoverCard>
      ) : (
        label
      );
    }
    default:
      return <span title={subject}>{subject}</span>;
  }
}
