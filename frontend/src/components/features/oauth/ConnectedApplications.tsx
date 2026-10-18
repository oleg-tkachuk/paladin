"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import { userClient } from "@/lib/connect/client";
import { Card } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { ListLoadError } from "@/components/ui/ListLoadError";
import {
  RecordDialog,
  RecordField,
  openableRow,
} from "@/components/ui/RecordDialog";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useNotification } from "@/components/ui/Notification";
import { RelativeTime } from "@/components/RelativeTime";
import { Timestamp } from "@/components/Timestamp";
import type { OAuthGrant } from "@/gen/paladin/iam/v1/user_service_pb";
import { errorMessage } from "@/hooks/errorContract";
import { describeOAuthScope, grantsWrite } from "@/lib/auth/oauthScopes";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

/** The query key of a user's grants. */
export function oauthGrantsKey(userName: string) {
  return ["oauthGrants", userName] as const;
}

// The columns of the table; the empty and loading rows span them.
const GRANT_COLUMNS = 6;
// A grant's access, as its badge names it: whether it may write.
const ACCESS = {
  write: { label: "read & write", variant: "warning" },
  read: { label: "read only", variant: "outline" },
} as const;

function access(g: OAuthGrant) {
  return grantsWrite(g.scopes) ? ACCESS.write : ACCESS.read;
}

/**
 * The OAuth clients a user has let in — agents and apps that authorized at
 * /oauth/authorize — one per row, opening to the whole grant, and a way to cut
 * each off (ADR-0035). Ending a grant stops its refresh tokens at once; an
 * access token the client already holds lives out its few minutes.
 */
export function ConnectedApplications({
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
  const [opened, setOpened] = useState<OAuthGrant | null>(null);
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

  const startRevoke = (g: OAuthGrant) => {
    setOpened(null);
    setTarget(g);
  };

  return (
    <>
      {query.isError ? (
        <ListLoadError
          what="the connected applications"
          reason={errorMessage(query.error, "the request failed")}
          onRetry={() => void query.refetch()}
        />
      ) : (
        <Card className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Application</TableHead>
                <TableHead>Access</TableHead>
                <TableHead className="hidden @lg:table-cell">Plane</TableHead>
                <TableHead className="hidden @2xl:table-cell">
                  Granted
                </TableHead>
                <TableHead className="hidden @xl:table-cell">
                  Last used
                </TableHead>
                <TableHead className="w-24 text-right">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {query.isPending ? (
                <TableRow>
                  <TableCell colSpan={GRANT_COLUMNS}>
                    <Skeleton className="h-8 w-full" />
                  </TableCell>
                </TableRow>
              ) : grants.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={GRANT_COLUMNS}
                    className="whitespace-normal py-8 text-center text-muted-foreground"
                  >
                    {own ? "You have" : "This user has"} not let any application
                    in. An agent that authorizes through OAuth — one connected
                    to the MCP server, say — appears here.
                  </TableCell>
                </TableRow>
              ) : (
                grants.map((g) => (
                  <TableRow key={g.id} {...openableRow(() => setOpened(g))}>
                    <TableCell
                      className={cn(T.code, "whitespace-normal break-all")}
                    >
                      {g.clientId}
                    </TableCell>
                    <TableCell>
                      <Badge variant={access(g).variant}>
                        {access(g).label}
                      </Badge>
                    </TableCell>
                    <TableCell className={cn("hidden @lg:table-cell", T.code)}>
                      {g.audience}
                    </TableCell>
                    <TableCell className="hidden @2xl:table-cell">
                      <RelativeTime ts={g.grantedAt} />
                    </TableCell>
                    <TableCell className="hidden @xl:table-cell">
                      <RelativeTime ts={g.refreshedAt} />
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => startRevoke(g)}
                      >
                        Revoke
                      </Button>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </Card>
      )}

      <RecordDialog
        open={opened !== null}
        onOpenChange={(o) => !o && setOpened(null)}
        title="Connected application"
        subtitle={opened?.clientId}
      >
        {opened ? (
          <>
            <RecordField label="Access">
              <Badge variant={access(opened).variant}>
                {access(opened).label}
              </Badge>
            </RecordField>
            <RecordField label="May">
              <ul className="space-y-0.5">
                {opened.scopes.length === 0 ? (
                  <li>No scope</li>
                ) : (
                  opened.scopes.map((s) => (
                    <li key={s}>{describeOAuthScope(s)}</li>
                  ))
                )}
              </ul>
            </RecordField>
            <RecordField label="Plane" mono>
              {opened.audience}
            </RecordField>
            <RecordField label="Granted">
              <Timestamp ts={opened.grantedAt} />
            </RecordField>
            <RecordField label="Last refreshed">
              <Timestamp ts={opened.refreshedAt} />
            </RecordField>
            <RecordField label="Expires">
              <Timestamp ts={opened.expiresAt} />
            </RecordField>
            <div className="flex justify-end pt-2">
              <Button
                variant="destructive"
                size="sm"
                onClick={() => startRevoke(opened)}
              >
                Revoke access
              </Button>
            </div>
          </>
        ) : null}
      </RecordDialog>

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
    </>
  );
}
