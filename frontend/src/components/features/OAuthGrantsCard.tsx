"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import { userClient } from "@/lib/connect/client";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { useNotification } from "@/components/ui/Notification";
import { Timestamp } from "@/components/Timestamp";
import type { OAuthGrant } from "@/gen/paladin/iam/v1/user_service_pb";
import { errorMessage } from "@/hooks/errorContract";
import { describeOAuthScope } from "@/lib/auth/oauthScopes";
import { T } from "@/lib/ui/typography";

/** The query key of a user's grants. */
export function oauthGrantsKey(userName: string) {
  return ["oauthGrants", userName] as const;
}

/**
 * The OAuth clients a user has let in — agents and apps that authorized at
 * /oauth/authorize — and a way to cut each off (ADR-0035). Ending a grant
 * stops its refresh tokens at once; an access token the client already holds
 * lives out its few minutes.
 */
export function OAuthGrantsCard({
  userName,
  own,
}: {
  /** The user's resource name, tenants/<tenant>/users/<user>. */
  userName: string;
  /** The signed-in user's own grants, which changes the wording only. */
  own: boolean;
}) {
  const queryClient = useQueryClient();
  const { showNotification } = useNotification();
  const [target, setTarget] = useState<OAuthGrant | null>(null);

  const query = useQuery({
    queryKey: oauthGrantsKey(userName),
    queryFn: ({ signal }) =>
      userClient.listOAuthGrants({ name: userName }, { signal }),
    enabled: !!userName,
  });
  const grants = query.data?.grants ?? [];

  const revoke = async () => {
    if (!target) return;
    try {
      await userClient.revokeOAuthGrant({
        name: userName,
        grantId: target.id,
      });
      showNotification({
        type: "success",
        title: "Access revoked",
        message: `${target.clientId} can no longer refresh its access.`,
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Revoke failed",
        message: errorMessage(err, "The grant was not revoked"),
      });
      throw err;
    } finally {
      void queryClient.invalidateQueries({
        queryKey: oauthGrantsKey(userName),
      });
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Connected applications</CardTitle>
        <CardDescription>
          {own ? "Clients you" : "Clients this user"} let in through OAuth, such
          as an agent connected to the MCP server.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {query.isPending ? (
          <Skeleton className="h-10 w-full" />
        ) : query.isError ? (
          <ListLoadError
            what="the connected applications"
            reason={errorMessage(query.error, "the request failed")}
            onRetry={() => void query.refetch()}
          />
        ) : grants.length === 0 ? (
          <p className={T.hint}>
            {own ? "You have" : "This user has"} not let any application in.
          </p>
        ) : (
          <ul className="divide-y divide-border">
            {grants.map((g) => (
              <li
                key={g.id}
                className="flex flex-wrap items-center justify-between gap-3 py-3"
              >
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className={T.code}>{g.clientId}</span>
                    <Badge variant="outline" className={T.labelTight}>
                      {g.audience}
                    </Badge>
                  </div>
                  <div className={T.hint}>
                    {g.scopes.map(describeOAuthScope).join("; ") || "No scope"}
                  </div>
                  <div className={T.hint}>
                    Granted <Timestamp ts={g.grantedAt} />, last refreshed{" "}
                    <Timestamp ts={g.refreshedAt} />
                  </div>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setTarget(g)}
                >
                  Revoke
                </Button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
      <ConfirmModal
        isOpen={target !== null}
        onClose={() => setTarget(null)}
        onConfirm={revoke}
        type="danger"
        title="Revoke access?"
        message={`${target?.clientId ?? "The client"} will have to be authorized again to reach Paladin.`}
        warning="It cannot refresh its access after this. A token it already holds keeps working for a few minutes."
        confirmText="Revoke"
      />
    </Card>
  );
}
