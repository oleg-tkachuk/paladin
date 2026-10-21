"use client";

// /users — platform-wide user index. Cross-tenant listing via
// IAM.ListUsers(parent="") gated to platform.admin server-side.
// Operators can see who has which roles across all tenants; each user
// opens on a page of their own, where roles and scopes are edited.

import { usePageAction } from "@/context/ActionsContext";
import { CREATE_ACTION } from "@/lib/paletteActions";
import { IntentTableRow } from "@/components/ui/IntentTableRow";
import { USERS_LIST_QUERY_KEY, userDetailQuery } from "@/lib/queries/details";
import { ListBody, ListPage } from "@/components/layout/templates/ListPage";
import { emptyReason } from "@/lib/emptyState";
import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowPathIcon,
  PlusIcon,
  UserCircleIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { UserCreateDialog } from "@/components/features/users/UserCreateDialog";
import {
  UserRowActions,
  PasswordResetResult,
  type ResetTarget,
} from "@/components/features/users/UserRowActions";
import { listAllPages } from "@/lib/listAllPages";
import { userClient } from "@/lib/connect/client";
import { userHref } from "@/lib/userPath";
import { useTenants } from "@/hooks/useTenants";
import { normalizeError } from "@/lib/connect/error";
import { API_PAGE_SIZE_MAX } from "@/constants";

import { Button } from "@/components/ui/button";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { SortableHead, StaticHead } from "@/components/ui/SortHeader";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { User } from "@/gen/paladin/iam/v1/types_pb";
import { useTableView } from "@/hooks/useTableView";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { useTableFacets } from "@/hooks/useTableFacets";
import { useTablePage } from "@/hooks/useTablePage";
import { TablePagination } from "@/components/ui/TablePagination";
import type { Facet } from "@/lib/table/facets";
import type { SearchText, SortAccessors } from "@/lib/table/view";
import { StatusTone } from "@/lib/ui/status";
import { failedRead } from "@/components/ui/ListLoadError";
import { Badge } from "@/components/ui/badge";
import { Table, TableCell, TableHeader, TableRow } from "@/components/ui/table";
import { RelativeTime } from "@/components/RelativeTime";
import { cn } from "@/lib/utils";
import { TableScope } from "@/components/ui/TableScope";
import { TenantHoverCard } from "@/components/features/hover/ResourceHoverCards";

/** This list's id in the user's table preferences. */
const TABLE_ID = "users";

// The columns of the users table; the placeholder rows span them.
const USER_COLUMNS = 6;

// Whether a user may sign in: the one standing a user has.
type UserStanding = "active" | "disabled";
const standing = (u: User): UserStanding =>
  u.disabled ? "disabled" : "active";
// A disabled user is one an operator stopped on purpose, not a fault.
const USER_FACETS: readonly Facet<User>[] = [
  {
    id: "state",
    label: "State",
    values: (u) => [standing(u)],
    options: [
      { value: "active", label: "Active", tone: StatusTone.Success },
      { value: "disabled", label: "Disabled", tone: StatusTone.Suspended },
    ],
  },
];

type UserColumn = "user" | "tenant" | "login" | "state";

export default function UsersPage() {
  const {
    tenants,
    error: tenantsError,
    refetch: refetchTenants,
  } = useTenants();
  const [createOpen, setCreateOpen] = useState(false);
  // The palette offers the create while this page is on screen.
  usePageAction({
    id: CREATE_ACTION.user,
    label: "New user",
    description: "A user of a tenant",
    icon: PlusIcon,
    category: "create",
    perform: () => setCreateOpen(true),
  });
  // The generated password is returned once and never again, so it is held
  // here until the operator dismisses it rather than shown in a toast that
  // can be missed.
  const [resetTarget, setResetTarget] = useState<ResetTarget | null>(null);

  const usersQuery = useQuery({
    queryKey: USERS_LIST_QUERY_KEY,
    // Every page: the search below runs over these rows.
    queryFn: ({ signal }) =>
      listAllPages((pageToken) =>
        userClient
          .listUsers(
            { parent: "", page: { pageSize: API_PAGE_SIZE_MAX, pageToken } },
            { signal },
          )
          .then((res) => ({
            items: res.users,
            next: res.page?.nextPageToken ?? "",
          })),
      ),
  });
  // useMemo keeps the fallback array stable so downstream memos don't churn.
  const users = useMemo(() => usersQuery.data ?? [], [usersQuery.data]);
  const loading = usersQuery.isFetching;
  const error = usersQuery.error
    ? normalizeError(usersQuery.error).message
    : null;
  const refreshUsers = () => void usersQuery.refetch();

  const tenantByID = useMemo(
    () => new Map(tenants.map((t) => [t.tenantId, t])),
    [tenants],
  );

  const facets = useTableFacets(users, USER_FACETS, PAGE_TABLE);
  // A user's tenant sorts and is searched by its slug, which only the tenant
  // list knows.
  const sortBy = useMemo<SortAccessors<User, UserColumn>>(
    () => ({
      user: (u) => u.displayName || u.subject,
      tenant: (u) => tenantByID.get(u.tenantId)?.slug ?? u.tenantId,
      login: (u) => (u.lastLoginAt ? timestampDate(u.lastLoginAt) : undefined),
      state: standing,
    }),
    [tenantByID],
  );
  const searchIn = useMemo<SearchText<User>>(
    () => (u) => [
      u.subject,
      u.displayName,
      u.roles.join(" "),
      tenantByID.get(u.tenantId)?.slug,
    ],
    [tenantByID],
  );
  const view = useTableView(facets.rows, {
    text: searchIn,
    accessors: sortBy,
    url: PAGE_TABLE,
    tableId: TABLE_ID,
  });
  const filtered = view.rows;
  const paged = useTablePage(filtered, {
    url: PAGE_TABLE,
    tableId: TABLE_ID,
    resetOn: [view.query, facets.selection],
  });
  const sortProps = { current: view.sort, onSort: view.toggleSort };

  return (
    <TableScope id={TABLE_ID} sort={view.sort}>
      <ListPage
        footer={<TablePagination {...paged.pagination} />}
        header={
          <PageHeader
            title="Users"
            description="Cross-tenant view of IAM principals and their roles."
            showDefaultActions={false}
          />
        }
        toolbar={
          <TableToolbar
            query={view.query}
            onQueryChange={view.setQuery}
            searchLabel="Search users"
            placeholder="Search subject, display name, role, tenant slug…"
            facets={facets.controls}
            count={
              usersQuery.data
                ? { shown: filtered.length, total: users.length }
                : undefined
            }
            actions={
              <>
                <Button
                  variant="outline"
                  size="icon"
                  onClick={refreshUsers}
                  aria-label="Refresh"
                >
                  <ArrowPathIcon
                    className={cn("size-4", loading && "animate-spin")}
                  />
                </Button>
                <Button onClick={() => setCreateOpen(true)}>New user</Button>
              </>
            }
          />
        }
      >
        <Table>
          <TableHeader>
            <TableRow>
              <SortableHead
                label="User"
                column="user"
                className="@md:w-65"
                {...sortProps}
              />
              <SortableHead
                label="Tenant"
                column="tenant"
                className="hidden @lg:table-cell"
                {...sortProps}
              />
              <StaticHead className="hidden @md:table-cell">Roles</StaticHead>
              <SortableHead
                label="Last login"
                column="login"
                className="hidden @md:table-cell"
                {...sortProps}
              />
              <SortableHead
                label="State"
                column="state"
                className="w-20"
                {...sortProps}
              />
              <StaticHead className="text-right @md:w-30">Actions</StaticHead>
            </TableRow>
          </TableHeader>
          <ListBody
            colSpan={USER_COLUMNS}
            loading={loading && users.length === 0}
            error={
              error
                ? { what: "Users", reason: error, onRetry: refreshUsers }
                : null
            }
            empty={
              filtered.length === 0
                ? {
                    icon: UserCircleIcon,
                    what: "users",
                    reason: emptyReason({ filtered: users.length > 0 }),
                    onClearFilters: () => {
                      view.setQuery("");
                      facets.clear();
                    },
                  }
                : null
            }
          >
            {paged.rows.map((u) => {
              const tenant = tenantByID.get(u.tenantId);
              return (
                <IntentTableRow
                  key={u.userId}
                  className="group"
                  query={userDetailQuery(u.name)}
                >
                  <TableCell>
                    <div className="flex items-center gap-3">
                      <div className="flex size-8 items-center justify-center rounded-full bg-secondary text-xs font-semibold text-secondary-foreground">
                        {(u.displayName || u.subject || "—")
                          .slice(0, 2)
                          .toUpperCase()}
                      </div>
                      <div className="min-w-0">
                        <Link
                          href={userHref(u.name)}
                          className="block truncate font-medium hover:text-primary hover:underline"
                        >
                          {u.displayName || (
                            <span className="text-muted-foreground italic">
                              (no name)
                            </span>
                          )}
                        </Link>
                        <p className="truncate font-mono text-sm text-muted-foreground">
                          {u.subject}
                        </p>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className="hidden @lg:table-cell">
                    {tenant ? (
                      <TenantHoverCard handle={tenant.slug}>
                        <Link
                          href={`/tenants/${encodeURIComponent(tenant.slug)}`}
                          className="text-xs hover:text-primary hover:underline"
                        >
                          {tenant.slug}
                        </Link>
                      </TenantHoverCard>
                    ) : (
                      <span className="font-mono text-sm text-muted-foreground">
                        {u.tenantId.slice(0, 8)}…
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="hidden @md:table-cell">
                    <div className="flex flex-wrap gap-1">
                      {u.roles.length === 0 ? (
                        <span className="text-xs text-muted-foreground">—</span>
                      ) : (
                        u.roles.map((r) => (
                          <Badge
                            key={r}
                            variant="outline"
                            className="font-mono"
                          >
                            {r}
                          </Badge>
                        ))
                      )}
                    </div>
                  </TableCell>
                  <TableCell className="hidden @md:table-cell">
                    <RelativeTime ts={u.lastLoginAt} />
                  </TableCell>
                  <TableCell>
                    {u.disabled ? (
                      <Badge variant="destructive">disabled</Badge>
                    ) : (
                      <Badge variant="outline">active</Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-right">
                    <UserRowActions
                      user={u}
                      onChanged={refreshUsers}
                      onResetPassword={setResetTarget}
                    />
                  </TableCell>
                </IntentTableRow>
              );
            })}
          </ListBody>
        </Table>
      </ListPage>

      <UserCreateDialog
        isOpen={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={refreshUsers}
        tenants={tenants.map((t) => ({
          tenantId: t.tenantId,
          slug: t.slug,
          displayName: t.displayName,
        }))}
        tenantsFailed={failedRead(tenantsError, refetchTenants)}
      />
      <PasswordResetResult
        target={resetTarget}
        onClose={() => setResetTarget(null)}
      />
    </TableScope>
  );
}
