"use client";

// /users/<tenant>/<user> — one user: who they are, their roles and their
// scopes, with the same disable / reset / delete actions as the list. The
// path mirrors the resource name, tenants/<tenant>/users/<user>.

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { ArrowLeftIcon } from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { RelativeTime } from "@/components/RelativeTime";
import {
  PasswordResetResult,
  UserRowActions,
  type ResetTarget,
} from "@/components/features/users/UserRowActions";
import { UserRolesCard } from "@/components/features/users/UserRolesCard";
import { UserScopesCard } from "@/components/features/users/UserScopesCard";
import { userClient } from "@/lib/connect/client";
import { normalizeError } from "@/lib/connect/error";
import { T } from "@/lib/ui/typography";
import { USERS_INDEX_HREF, userResourceName } from "@/lib/userPath";

function Fact({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="grid grid-cols-[8rem_1fr] gap-3 text-sm">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-all">{children}</dd>
    </div>
  );
}

export default function UserDetailPage() {
  const router = useRouter();
  const params = useParams<{ tenant: string; user: string }>();
  const tenant = decodeURIComponent(params?.tenant ?? "");
  const userId = decodeURIComponent(params?.user ?? "");
  const name = userResourceName(tenant, userId);
  const [resetTarget, setResetTarget] = useState<ResetTarget | null>(null);

  const query = useQuery({
    queryKey: ["user", name],
    queryFn: ({ signal }) => userClient.getUser({ name }, { signal }),
  });
  const user = query.data;
  const refresh = () => void query.refetch();

  return (
    <div className="space-y-6">
      <PageHeader
        title={user ? user.displayName || user.subject : "User"}
        description={user ? user.subject : name}
        showDefaultActions={false}
        actions={
          <div className="flex items-center gap-2">
            {user && (
              <UserRowActions
                user={user}
                onChanged={refresh}
                onDeleted={() => router.push(USERS_INDEX_HREF)}
                onResetPassword={setResetTarget}
              />
            )}
            <Button variant="outline" size="sm" asChild>
              <Link href={USERS_INDEX_HREF}>
                <ArrowLeftIcon className="size-4" />
                All users
              </Link>
            </Button>
          </div>
        }
      />

      {query.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : query.error ? (
        <Card className="p-6">
          <ListLoadError
            what="User"
            reason={normalizeError(query.error).message}
            onRetry={refresh}
          />
        </Card>
      ) : user ? (
        <>
          <Card className="space-y-2 p-5">
            <h2 className="text-sm font-semibold">Identity</h2>
            <dl className="space-y-1.5">
              <Fact label="Subject">
                <span className="font-mono text-xs">{user.subject}</span>
              </Fact>
              <Fact label="User ID">
                <span className="font-mono text-xs">{user.userId}</span>
              </Fact>
              <Fact label="Tenant">
                <Link
                  href={`/tenants/${encodeURIComponent(user.tenantId)}`}
                  className="font-mono text-xs hover:text-primary hover:underline"
                >
                  {user.tenantId}
                </Link>
              </Fact>
              <Fact label="State">
                {user.disabled ? (
                  <Badge variant="destructive" className={T.labelTight}>
                    disabled
                  </Badge>
                ) : (
                  <Badge variant="outline" className={T.labelTight}>
                    active
                  </Badge>
                )}
              </Fact>
              <Fact label="Last login">
                <RelativeTime ts={user.lastLoginAt} />
              </Fact>
              <Fact label="Created">
                <RelativeTime ts={user.createdAt} />
              </Fact>
            </dl>
          </Card>
          <UserRolesCard user={user} onChanged={refresh} />
          <UserScopesCard user={user} onChanged={refresh} />
        </>
      ) : null}

      <PasswordResetResult
        target={resetTarget}
        onClose={() => setResetTarget(null)}
      />
    </div>
  );
}
