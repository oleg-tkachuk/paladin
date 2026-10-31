"use client";

// /capabilities — every tenant's capabilities in one list, newest first: the
// platform's index beside Buckets and Collections, for a caller spanning
// tenants. Each row names its tenant and opens in that tenant's workspace,
// where it is revoked or inspected; the states narrow the listing on the
// server, the search the rows read.
import { useMemo } from "react";
import Link from "next/link";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { ShieldCheckIcon } from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
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
import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import { errorMessage } from "@/hooks/errorContract";
import { useServerFacets } from "@/hooks/useTableFacets";
import { useTableView } from "@/hooks/useTableView";
import { principalKindLabel } from "@/app/tenants/[id]/capabilities/_constants";
import {
  CAPABILITY_FACETS,
  capabilityLifecycle,
  dispositionsFor,
} from "@/lib/capabilityLifecycle";
import { emptyReason } from "@/lib/emptyState";
import { capabilityHref, tenantHref } from "@/lib/navigation/hrefs";
import { platformCapabilitiesQuery } from "@/lib/queries/platformCapabilities";
import { tenantsQuery } from "@/lib/queries/tenants";
import { FACET_COUNTING, narrowedFromInitial } from "@/lib/table/facets";
import { LIFECYCLE_BADGE } from "@/lib/table/lifecycle";
import { PAGE_TABLE } from "@/lib/table/urlState";
import type { SortAccessors } from "@/lib/table/view";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

// The columns of the table; the empty and loading rows span them.
const COLUMNS = 6;
const STATE_FACET = CAPABILITY_FACETS[0].id;
// The facets are not counted: the listing pages, and a count would take a
// read of every page.
const NO_COUNTS = new Map();
// Optional columns appear by the width of the table's card.
const COL_CLIENT = "hidden @2xl:table-cell";
const COL_ISSUED = "hidden @3xl:table-cell";

type Column = "tenant" | "principal" | "state" | "issued" | "expires";
const ms = (t: Capability["issuedAt"]) =>
  t ? timestampDate(t).getTime() : undefined;

export default function PlatformCapabilitiesPage() {
  const facets = useServerFacets(CAPABILITY_FACETS, PAGE_TABLE);
  const dispositions = useMemo(
    () => dispositionsFor(facets.selection[STATE_FACET] ?? []),
    [facets.selection],
  );
  const query = useInfiniteQuery(platformCapabilitiesQuery(dispositions));
  // Names each row's tenant; trashed ones too, whose capabilities remain.
  const tenants = useQuery(tenantsQuery("all"));
  const tenantOf = useMemo(
    () => new Map((tenants.data ?? []).map((t) => [t.tenantId, t])),
    [tenants.data],
  );
  const rows = useMemo(
    () => query.data?.pages.flatMap((p) => p.capabilities) ?? [],
    [query.data],
  );
  const { tenantName, sortBy, searchIn } = useMemo(() => {
    const name = (c: Capability) => {
      const id = c.subject?.tenantId ?? "";
      const t = tenantOf.get(id);
      return t?.displayName || t?.slug || id;
    };
    const accessors: SortAccessors<Capability, Column> = {
      tenant: name,
      principal: (c) => c.subject?.subject ?? "",
      state: (c) => LIFECYCLE_BADGE[capabilityLifecycle(c)].label,
      issued: (c) => ms(c.issuedAt),
      expires: (c) => ms(c.expiresAt),
    };
    const search = (c: Capability) => [
      name(c),
      c.subject?.subject ?? "",
      c.clientId,
      c.id,
    ];
    return { tenantName: name, sortBy: accessors, searchIn: search };
  }, [tenantOf]);
  const view = useTableView(rows, {
    text: searchIn,
    accessors: sortBy,
    url: PAGE_TABLE,
  });
  const sortProps = { current: view.sort, onSort: view.toggleSort };
  const { controls } = facets;
  const facetControls = useMemo(
    () => controls(NO_COUNTS, FACET_COUNTING.withheld),
    [controls],
  );

  return (
    <div className="space-y-4">
      <PageHeader
        title="Capabilities"
        description="Every tenant's capabilities, newest first. Open one in its tenant's workspace to inspect or revoke it."
        showDefaultActions={false}
      />
      {query.isError ? (
        <ListLoadError
          what="the capabilities"
          reason={errorMessage(query.error, "the request failed")}
          onRetry={() => void query.refetch()}
        />
      ) : (
        <div className="space-y-3">
          <TableToolbar
            query={view.query}
            onQueryChange={view.setQuery}
            placeholder="Search by tenant, principal, client or id…"
            searchLabel="Search capabilities"
            facets={facetControls}
            count={
              query.isPending
                ? undefined
                : { shown: view.rows.length, total: rows.length }
            }
          />
          <Card className="@container p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <SortableHead label="Tenant" column="tenant" {...sortProps} />
                  <SortableHead
                    label="Principal"
                    column="principal"
                    {...sortProps}
                  />
                  <StaticHead className={COL_CLIENT}>Client</StaticHead>
                  <SortableHead label="State" column="state" {...sortProps} />
                  <SortableHead
                    label="Issued"
                    column="issued"
                    className={COL_ISSUED}
                    {...sortProps}
                  />
                  <SortableHead
                    label="Expires"
                    column="expires"
                    {...sortProps}
                  />
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
                    icon={ShieldCheckIcon}
                    what="capabilities"
                    reason={emptyReason({
                      filtered:
                        view.query !== "" ||
                        narrowedFromInitial(
                          CAPABILITY_FACETS,
                          facets.selection,
                        ),
                    })}
                    onClearFilters={() => {
                      view.setQuery("");
                      facets.clear();
                    }}
                    purpose="A capability is issued in a tenant's workspace, or exchanged by an agent connected through OAuth."
                  />
                ) : (
                  view.rows.map((c) => {
                    const tenant = c.subject?.tenantId ?? "";
                    const handle = tenantOf.get(tenant)?.slug || tenant;
                    return (
                      <TableRow key={c.id}>
                        <TableCell className="whitespace-normal">
                          <Link
                            href={tenantHref(handle)}
                            className="hover:underline"
                          >
                            {tenantName(c)}
                          </Link>
                        </TableCell>
                        <TableCell className="whitespace-normal">
                          <Link
                            href={capabilityHref(handle, c.id)}
                            className={cn(T.code, "break-all hover:underline")}
                          >
                            {c.subject?.subject || c.id}
                          </Link>
                          <span className="block text-xs text-muted-foreground">
                            {principalKindLabel(c.subject?.kind)}
                          </span>
                        </TableCell>
                        <TableCell className={cn(COL_CLIENT, T.code)}>
                          {c.clientId || "—"}
                        </TableCell>
                        <TableCell>
                          <LifecycleBadge state={capabilityLifecycle(c)} />
                        </TableCell>
                        <TableCell className={COL_ISSUED}>
                          <RelativeTime ts={c.issuedAt} />
                        </TableCell>
                        <TableCell>
                          <RelativeTime ts={c.expiresAt} />
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
      )}
    </div>
  );
}
