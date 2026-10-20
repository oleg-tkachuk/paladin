"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { timestampDate } from "@bufbuild/protobuf/wkt";

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
import { SortableHead } from "@/components/ui/SortHeader";
import { LifecycleBadge } from "@/components/ui/LifecycleBadge";
import { TableToolbar } from "@/components/ui/TableToolbar";
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
import {
  OAuthGrant_State,
  type OAuthGrant,
} from "@/gen/paladin/iam/v1/user_service_pb";
import { errorMessage } from "@/hooks/errorContract";
import { useTableView } from "@/hooks/useTableView";
import type { TableUrl } from "@/lib/table/urlState";
import { useTableFacets } from "@/hooks/useTableFacets";
import {
  LIFECYCLE_BADGE,
  lifecycleFacet,
  type Lifecycle,
} from "@/lib/table/lifecycle";
import type { SortAccessors } from "@/lib/table/view";
import { describeOAuthScope, grantsWrite } from "@/lib/auth/oauthScopes";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

// The list of applications keeps its search and sort in the URL under its own names, beside the
// page's other tables.
const APPS_TABLE: TableUrl = { namespace: "apps" };

/**
 * The query key of a user's grants: every one whose rows remain, ended ones
 * too, since the state facet counts each state over what was read.
 */
const oauthGrantsKey = (userName: string) => ["oauthGrants", userName] as const;

// The columns of the table; the empty and loading rows span them.
const GRANT_COLUMNS = 7;

// A grant's lifecycle state. UNSPECIFIED is a server too old to say, which
// only ever lists live grants.
const LIFECYCLE_OF = {
  [OAuthGrant_State.UNSPECIFIED]: "active",
  [OAuthGrant_State.ACTIVE]: "active",
  [OAuthGrant_State.REVOKED]: "revoked",
  [OAuthGrant_State.EXPIRED]: "expired",
} as const satisfies Record<OAuthGrant_State, Lifecycle>;
const lifecycle = (g: OAuthGrant): Lifecycle => LIFECYCLE_OF[g.state];
const isActive = (g: OAuthGrant) => lifecycle(g) === "active";
const GRANT_FACETS = [lifecycleFacet(lifecycle)];
// Kept whole while the first read is in flight, so the facets' memos hold.
const NO_GRANTS: readonly OAuthGrant[] = [];

type GrantColumn =
  "application" | "access" | "state" | "plane" | "granted" | "used";
const ms = (t: OAuthGrant["grantedAt"]) =>
  t ? timestampDate(t).getTime() : undefined;
const SORT_BY: SortAccessors<OAuthGrant, GrantColumn> = {
  application: (g) => g.clientId,
  access: (g) => access(g).label,
  state: (g) => LIFECYCLE_BADGE[lifecycle(g)].label,
  plane: (g) => g.audience,
  granted: (g) => ms(g.grantedAt),
  used: (g) => ms(g.refreshedAt),
};
const SEARCH_IN = (g: OAuthGrant) => [
  g.clientId,
  g.audience,
  g.scopes.join(" "),
  LIFECYCLE_BADGE[lifecycle(g)].label,
];
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
      userClient.listOAuthGrants(
        { name: userName, includeInactive: true },
        { signal },
      ),
    enabled: !!userName,
  });
  const grants = query.data?.grants ?? NO_GRANTS;
  const facets = useTableFacets(grants, GRANT_FACETS, APPS_TABLE);
  const view = useTableView(facets.rows, {
    text: SEARCH_IN,
    accessors: SORT_BY,
    url: APPS_TABLE,
  });
  const sortProps = { current: view.sort, onSort: view.toggleSort };

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
        <div className="space-y-3">
          <TableToolbar
            query={view.query}
            onQueryChange={view.setQuery}
            placeholder="Search by application, plane or scope…"
            searchLabel="Search connected applications"
            facets={facets.controls}
            count={
              query.isPending
                ? undefined
                : { shown: view.rows.length, total: grants.length }
            }
          />
          <Card className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <SortableHead
                    label="Application"
                    column="application"
                    {...sortProps}
                  />
                  <SortableHead label="Access" column="access" {...sortProps} />
                  <SortableHead label="State" column="state" {...sortProps} />
                  <SortableHead
                    label="Plane"
                    column="plane"
                    className="hidden @lg:table-cell"
                    {...sortProps}
                  />
                  <SortableHead
                    label="Granted"
                    column="granted"
                    className="hidden @2xl:table-cell"
                    {...sortProps}
                  />
                  <SortableHead
                    label="Last used"
                    column="used"
                    className="hidden @xl:table-cell"
                    {...sortProps}
                  />
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
                ) : view.rows.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={GRANT_COLUMNS}
                      className="whitespace-normal py-8 text-center text-muted-foreground"
                    >
                      {grants.length > 0
                        ? "No application matches your filters. Ended grants stay listed until their tokens are purged."
                        : `${own ? "You have" : "This user has"} not let any application in. An agent that authorizes through OAuth — one connected to the MCP server, say — appears here.`}
                    </TableCell>
                  </TableRow>
                ) : (
                  view.rows.map((g) => (
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
                      <TableCell>
                        <LifecycleBadge state={lifecycle(g)} />
                      </TableCell>
                      <TableCell
                        className={cn("hidden @lg:table-cell", T.code)}
                      >
                        {g.audience}
                      </TableCell>
                      <TableCell className="hidden @2xl:table-cell">
                        <RelativeTime ts={g.grantedAt} />
                      </TableCell>
                      <TableCell className="hidden @xl:table-cell">
                        <RelativeTime ts={g.refreshedAt} />
                      </TableCell>
                      <TableCell className="text-right">
                        {/* An ended grant has nothing left to revoke. */}
                        {isActive(g) ? (
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => startRevoke(g)}
                          >
                            Revoke
                          </Button>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </Card>
        </div>
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
            <RecordField label="State">
              <LifecycleBadge state={lifecycle(opened)} />
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
            {isActive(opened) ? (
              <div className="flex justify-end pt-2">
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => startRevoke(opened)}
                >
                  Revoke access
                </Button>
              </div>
            ) : null}
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
