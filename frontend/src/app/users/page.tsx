"use client";

// /users — platform-wide user index. Cross-tenant listing via
// IAM.ListUsers(parent="") gated to platform.admin server-side.
// Operators can see who has which roles across all tenants, jump
// to per-user detail (deferred: detail page itself), and trigger
// role changes via GrantScopes/RevokeScopes when those become
// reachable from the UI (BACKLOG).

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowPathIcon,
  MagnifyingGlassIcon,
  UserCircleIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { UserCreateDialog } from "@/components/features/users/UserCreateDialog";
import {
  UserRowActions,
  PasswordResetResult,
  type ResetTarget,
} from "@/components/features/users/UserRowActions";
import { userClient } from "@/lib/connect/client";
import { useTenants } from "@/hooks/useTenants";
import { normalizeError } from "@/lib/connect/error";
import { API_PAGE_SIZE_MAX } from "@/constants";

import { Button } from "@/components/ui/button";
import { failedRead, ListLoadError } from "@/components/ui/ListLoadError";
import { Input } from "@/components/ui/input";
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
import { T } from "@/lib/ui/typography";

export default function UsersPage() {
  const { tenants, error: tenantsError, fetchTenants } = useTenants();
  const [search, setSearch] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  // The generated password is returned once and never again, so it is held
  // here until the operator dismisses it rather than shown in a toast that
  // can be missed.
  const [resetTarget, setResetTarget] = useState<ResetTarget | null>(null);

  const usersQuery = useQuery({
    queryKey: ["users"],
    queryFn: ({ signal }) =>
      userClient
        .listUsers(
          { parent: "", page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" } },
          { signal },
        )
        .then((res) => res.users),
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

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return users;
    return users.filter(
      (u) =>
        u.subject.toLowerCase().includes(q) ||
        (u.displayName || "").toLowerCase().includes(q) ||
        u.roles.some((r) => r.toLowerCase().includes(q)) ||
        (tenantByID.get(u.tenantId)?.slug || "").toLowerCase().includes(q),
    );
  }, [users, search, tenantByID]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Users"
        description="Cross-tenant view of IAM principals and their roles."
        showDefaultActions={false}
      />

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[240px]">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Search subject, display name, role, tenant slug…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <Button
          variant="outline"
          size="icon"
          onClick={refreshUsers}
          aria-label="Refresh"
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
        <Button onClick={() => setCreateOpen(true)}>New user</Button>
      </div>

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[260px]">User</TableHead>
              <TableHead>Tenant</TableHead>
              <TableHead>Roles</TableHead>
              <TableHead className="hidden md:table-cell">Last login</TableHead>
              <TableHead className="w-[80px]">State</TableHead>
              <TableHead className="w-[120px] text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && users.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={6} className="py-3">
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
                <TableCell colSpan={6} className="h-32 text-center">
                  <ListLoadError
                    what="Users"
                    reason={error}
                    onRetry={refreshUsers}
                  />
                </TableCell>
              </TableRow>
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="h-32 text-center">
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <UserCircleIcon className="size-8 opacity-40" />
                    <p className="text-sm">
                      {search ? "No users match your search." : "No users yet."}
                    </p>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((u) => {
                const tenant = tenantByID.get(u.tenantId);
                return (
                  <TableRow key={u.userId} className="group">
                    <TableCell>
                      <div className="flex items-center gap-3">
                        <div className="flex size-8 items-center justify-center rounded-full bg-secondary text-xs font-semibold text-secondary-foreground">
                          {(u.displayName || u.subject || "—")
                            .slice(0, 2)
                            .toUpperCase()}
                        </div>
                        <div className="min-w-0">
                          <p className="truncate font-medium">
                            {u.displayName || (
                              <span className="text-muted-foreground italic">
                                (no name)
                              </span>
                            )}
                          </p>
                          <p className="truncate font-mono text-[11px] text-muted-foreground">
                            {u.subject}
                          </p>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell>
                      {tenant ? (
                        <Link
                          href={`/tenants/${encodeURIComponent(tenant.slug)}`}
                          className="text-xs hover:text-primary hover:underline"
                        >
                          {tenant.slug}
                        </Link>
                      ) : (
                        <span className="font-mono text-[10px] text-muted-foreground">
                          {u.tenantId.slice(0, 8)}…
                        </span>
                      )}
                    </TableCell>
                    <TableCell>
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
                              className={cn(T.labelTight, "font-mono")}
                            >
                              {r}
                            </Badge>
                          ))
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <RelativeTime ts={u.lastLoginAt} />
                    </TableCell>
                    <TableCell>
                      {u.disabled ? (
                        <Badge variant="destructive" className={T.labelTight}>
                          disabled
                        </Badge>
                      ) : (
                        <Badge variant="outline" className={T.labelTight}>
                          active
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <UserRowActions
                        user={u}
                        onChanged={refreshUsers}
                        onResetPassword={setResetTarget}
                      />
                    </TableCell>
                  </TableRow>
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
  );
}
