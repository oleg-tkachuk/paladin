"use client";

// /users — platform-wide user index. Cross-tenant listing via
// IAM.ListUsers(parent="") gated to platform.admin server-side.
// Operators can see who has which roles across all tenants; each user
// opens on a page of their own, where roles and scopes are edited.

import { usePageAction } from "@/context/ActionsContext";
import { CREATE_ACTION } from "@/lib/paletteActions";
import { IntentTableRow } from "@/components/ui/IntentTableRow";
import { userDetailQuery } from "@/lib/queries/details";
import { EmptyTableRow } from "@/components/ui/EmptyState";
import { emptyReason } from "@/lib/emptyState";
import React, { useEffect, useMemo, useState } from "react";
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
import {
  TableToolbar,
  type ToolbarFilterOption,
} from "@/components/ui/TableToolbar";
import { SortableHead } from "@/components/ui/SortHeader";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { User } from "@/gen/paladin/iam/v1/types_pb";
import { useTableView } from "@/hooks/useTableView";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { useTableFilter } from "@/hooks/useTableUrl";
import type { SearchText, SortAccessors } from "@/lib/table/view";
import { failedRead, ListLoadError } from "@/components/ui/ListLoadError";
import { Card } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { RelativeTime } from "@/components/RelativeTime";
import { cn } from "@/lib/utils";
import { TableScope } from "@/components/ui/TableScope";

/** This list's id in the user's table preferences. */
const TABLE_ID = "users";

// The columns of the users table; the placeholder rows span them.
const USER_COLUMNS = 6;

// Whether a user may sign in: the one standing a user has.
type UserStanding = "active" | "disabled";
const standing = (u: User): UserStanding =>
  u.disabled ? "disabled" : "active";
type StandingFilter = UserStanding | "all";
const STANDING_FILTERS: readonly ToolbarFilterOption<StandingFilter>[] = [
  { value: "all", label: "All" },
  { value: "active", label: "Active" },
  { value: "disabled", label: "Disabled" },
];
const DEFAULT_STANDING_FILTER: StandingFilter = "all";

type UserColumn = "user" | "tenant" | "login" | "state";

export default function UsersPage() {
  const { tenants, error: tenantsError, fetchTenants } = useTenants();
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
    queryKey: ["users"],
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

  // useTenants has no auto-fetch (caller-triggered); kick it on mount. Not a
  // set-state-in-effect hit — fetchTenants is a cross-module useCallback.
  useEffect(() => {
    void fetchTenants();
  }, [fetchTenants]);

  const tenantByID = useMemo(
    () => new Map(tenants.map((t) => [t.tenantId, t])),
    [tenants],
  );

  const [standingFilter, setStandingFilter] = useTableFilter(
    STANDING_FILTERS,
    DEFAULT_STANDING_FILTER,
    PAGE_TABLE,
  );
  const inStanding = useMemo(
    () =>
      standingFilter === DEFAULT_STANDING_FILTER
        ? users
        : users.filter((u) => standing(u) === standingFilter),
    [users, standingFilter],
  );
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
  const view = useTableView(inStanding, {
    text: searchIn,
    accessors: sortBy,
    url: PAGE_TABLE,
    tableId: TABLE_ID,
  });
  const filtered = view.rows;
  const sortProps = { current: view.sort, onSort: view.toggleSort };

  return (
    <TableScope id={TABLE_ID} sort={view.sort}>
      <div className="space-y-6">
        <PageHeader
          title="Users"
          description="Cross-tenant view of IAM principals and their roles."
          showDefaultActions={false}
        />

        <TableToolbar
          query={view.query}
          onQueryChange={view.setQuery}
          searchLabel="Search users"
          placeholder="Search subject, display name, role, tenant slug…"
          filter={{
            label: "Standing",
            options: STANDING_FILTERS,
            value: standingFilter,
            onChange: setStandingFilter,
          }}
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

        <Card className="p-0">
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
                <TableHead className="hidden @md:table-cell">Roles</TableHead>
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
                <TableHead className="text-right @md:w-30">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading && users.length === 0 ? (
                [0, 1, 2].map((i) => (
                  <TableRow key={`s-${i}`}>
                    <TableCell colSpan={USER_COLUMNS} className="py-3">
                      <Skeleton className="h-7 w-full" />
                    </TableCell>
                  </TableRow>
                ))
              ) : error ? (
                // The error used to be a banner ABOVE this table while the table
                // itself still said "No users yet". Both were on screen at once,
                // and the eye goes to the table — which was asserting the list is
                // empty when it is unknown. One statement, in the place the rows
                // would have been, matching /buckets and /tenants.
                <TableRow>
                  <TableCell
                    colSpan={USER_COLUMNS}
                    className="h-32 text-center"
                  >
                    <ListLoadError
                      what="Users"
                      reason={error}
                      onRetry={refreshUsers}
                    />
                  </TableCell>
                </TableRow>
              ) : filtered.length === 0 ? (
                <EmptyTableRow
                  colSpan={USER_COLUMNS}
                  icon={UserCircleIcon}
                  what="users"
                  reason={emptyReason({ filtered: users.length > 0 })}
                />
              ) : (
                filtered.map((u) => {
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
                          <Link
                            href={`/tenants/${encodeURIComponent(tenant.slug)}`}
                            className="text-xs hover:text-primary hover:underline"
                          >
                            {tenant.slug}
                          </Link>
                        ) : (
                          <span className="font-mono text-sm text-muted-foreground">
                            {u.tenantId.slice(0, 8)}…
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="hidden @md:table-cell">
                        <div className="flex flex-wrap gap-1">
                          {u.roles.length === 0 ? (
                            <span className="text-xs text-muted-foreground">
                              —
                            </span>
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
                })
              )}
            </TableBody>
          </Table>
        </Card>

        <UserCreateDialog
          isOpen={createOpen}
          onClose={() => setCreateOpen(false)}
          onCreated={refreshUsers}
          tenants={tenants.map((t) => ({
            tenantId: t.tenantId,
            slug: t.slug,
            displayName: t.displayName,
          }))}
          tenantsFailed={failedRead(tenantsError, fetchTenants)}
        />
        <PasswordResetResult
          target={resetTarget}
          onClose={() => setResetTarget(null)}
        />
      </div>
    </TableScope>
  );
}
