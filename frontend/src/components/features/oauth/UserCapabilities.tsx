"use client";

// The capabilities acting on a user's behalf: what each agent the user let in
// through OAuth may do as them, and until when. Read from IAM, so it works
// without the admin plane; the states narrow the listing on the server, the
// search the rows read.
import { useMemo } from "react";
import Link from "next/link";
import { useInfiniteQuery } from "@tanstack/react-query";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { IdentificationIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { EmptyTableRow } from "@/components/ui/EmptyState";
import { LifecycleBadge } from "@/components/ui/LifecycleBadge";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { SortableHead, StaticHead } from "@/components/ui/SortHeader";
import { TableToolbar } from "@/components/ui/TableToolbar";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { RelativeTime } from "@/components/RelativeTime";
import type { UserCapability } from "@/gen/paladin/iam/v1/user_service_pb";
import { errorMessage } from "@/hooks/errorContract";
import { useServerFacets } from "@/hooks/useTableFacets";
import { useTableView } from "@/hooks/useTableView";
import { textParam, useUrlState } from "@/hooks/useUrlState";
import { emptyReason } from "@/lib/emptyState";
import { grantHref, ownGrantHref } from "@/lib/oauthGrants";
import { userCapabilitiesQuery } from "@/lib/queries/userCapabilities";
import { FACET_COUNTING, narrowedFromInitial } from "@/lib/table/facets";
import { LIFECYCLE_BADGE } from "@/lib/table/lifecycle";
import type { SortAccessors } from "@/lib/table/view";
import {
  USER_CAPABILITIES_GRANT_PARAM,
  USER_CAPABILITIES_TABLE,
  USER_CAPABILITY_FACETS,
  statesFor,
  userCapabilityLifecycle,
} from "@/lib/userCapabilities";
import { userTenant } from "@/lib/userPath";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

// The columns of the table; the empty and loading rows span them.
const COLUMNS = 6;
// The state facet's id, whose choices become the states asked for.
const STATE_FACET = USER_CAPABILITY_FACETS[0].id;
// The facets are not counted: the listing pages, and a count would take a
// read of every page.
const NO_COUNTS = new Map();
const GRANT_FILTER = textParam(USER_CAPABILITIES_GRANT_PARAM);

type Column = "application" | "state" | "issued" | "expires";
const ms = (t: UserCapability["issuedAt"]) =>
  t ? timestampDate(t).getTime() : undefined;
const SORT_BY: SortAccessors<UserCapability, Column> = {
  application: (c) => c.clientId,
  state: (c) => LIFECYCLE_BADGE[userCapabilityLifecycle(c)].label,
  issued: (c) => ms(c.issuedAt),
  expires: (c) => ms(c.expiresAt),
};
const SEARCH_IN = (c: UserCapability) => [
  c.clientId,
  c.operations.join(" "),
  c.audience.join(" "),
  c.revocationReason,
];

export function UserCapabilities({
  userName,
  own,
}: {
  /** The user's resource name, tenants/<tenant>/users/<user>. */
  userName: string;
  /** The signed-in user's own, which changes the wording and the links. */
  own: boolean;
}) {
  // A grant's page links here with its id: what revoking it revokes.
  const [grant, setGrant] = useUrlState(GRANT_FILTER, "");
  const facets = useServerFacets(
    USER_CAPABILITY_FACETS,
    USER_CAPABILITIES_TABLE,
  );
  const states = useMemo(
    () => statesFor(facets.selection[STATE_FACET] ?? []),
    [facets.selection],
  );
  const query = useInfiniteQuery(
    userCapabilitiesQuery({ userName, grant, states }),
  );
  const rows = useMemo(
    () => query.data?.pages.flatMap((p) => p.capabilities) ?? [],
    [query.data],
  );
  const view = useTableView(rows, {
    text: SEARCH_IN,
    accessors: SORT_BY,
    url: USER_CAPABILITIES_TABLE,
  });
  const sortProps = { current: view.sort, onSort: view.toggleSort };
  const tenant = userTenant(userName);
  const user = userName.split("/").at(-1) ?? "";
  const grantLink = (id: string) =>
    own ? ownGrantHref(id) : tenant ? grantHref(tenant, user, id) : null;
  const { controls } = facets;
  const facetControls = useMemo(
    () => controls(NO_COUNTS, FACET_COUNTING.withheld),
    [controls],
  );

  if (query.isError) {
    return (
      <ListLoadError
        what="the capabilities"
        reason={errorMessage(query.error, "the request failed")}
        onRetry={() => void query.refetch()}
      />
    );
  }

  return (
    <div className="space-y-3">
      <TableToolbar
        query={view.query}
        onQueryChange={view.setQuery}
        placeholder="Search by application, operation or plane…"
        searchLabel="Search capabilities"
        facets={facetControls}
        count={
          query.isPending
            ? undefined
            : { shown: view.rows.length, total: rows.length }
        }
      />
      {grant ? (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground">One grant&apos;s</span>
          <code className="font-mono text-xs break-all">{grant}</code>
          <Button variant="ghost" size="sm" onClick={() => setGrant("")}>
            Every grant
          </Button>
        </div>
      ) : null}
      <Card className="@container p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <SortableHead
                label="Application"
                column="application"
                {...sortProps}
              />
              <StaticHead>May</StaticHead>
              <SortableHead label="State" column="state" {...sortProps} />
              <SortableHead
                label="Issued"
                column="issued"
                className="hidden @xl:table-cell"
                {...sortProps}
              />
              <SortableHead label="Expires" column="expires" {...sortProps} />
              <StaticHead className="hidden @2xl:table-cell">Grant</StaticHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {query.isPending ? (
              <TableRow>
                <TableCell colSpan={COLUMNS}>
                  <Skeleton className="h-8 w-full" />
                </TableCell>
              </TableRow>
            ) : view.rows.length === 0 ? (
              <EmptyTableRow
                colSpan={COLUMNS}
                icon={IdentificationIcon}
                what="capabilities"
                reason={emptyReason({
                  filtered:
                    view.query !== "" ||
                    grant !== "" ||
                    narrowedFromInitial(
                      USER_CAPABILITY_FACETS,
                      facets.selection,
                    ),
                })}
                onClearFilters={() => {
                  view.setQuery("");
                  facets.clear();
                  setGrant("");
                }}
                purpose={`An agent ${own ? "you connect" : "this user connects"} through OAuth — to the MCP server, say — gets one here each time it exchanges its token.`}
              />
            ) : (
              view.rows.map((c) => {
                const state = userCapabilityLifecycle(c);
                const link = c.grantId ? grantLink(c.grantId) : null;
                return (
                  <TableRow key={c.id}>
                    <TableCell
                      className={cn(T.code, "whitespace-normal break-all")}
                    >
                      {c.clientId || "—"}
                    </TableCell>
                    <TableCell className="whitespace-normal">
                      <span className={T.code}>
                        {c.operations.join(", ") || "—"}
                      </span>
                      <span className="block text-xs text-muted-foreground">
                        on {c.audience.join(", ") || "—"}
                      </span>
                    </TableCell>
                    <TableCell className="whitespace-normal">
                      <LifecycleBadge state={state} />
                      {state === "revoked" && c.revocationReason ? (
                        <span className="block text-xs text-muted-foreground">
                          {c.revocationReason}
                        </span>
                      ) : null}
                    </TableCell>
                    <TableCell className="hidden @xl:table-cell">
                      <RelativeTime ts={c.issuedAt} />
                    </TableCell>
                    <TableCell>
                      <RelativeTime ts={c.expiresAt} />
                    </TableCell>
                    <TableCell className="hidden @2xl:table-cell">
                      {link ? (
                        <Link
                          href={link}
                          className="text-primary hover:underline"
                        >
                          Open grant
                        </Link>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>
      {query.hasNextPage ? (
        <Button
          variant="outline"
          size="sm"
          onClick={() => void query.fetchNextPage()}
          disabled={query.isFetchingNextPage}
        >
          {query.isFetchingNextPage ? "Loading…" : "Show more"}
        </Button>
      ) : null}
    </div>
  );
}
