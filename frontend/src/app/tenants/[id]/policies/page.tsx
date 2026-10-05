"use client";

// /tenants/<id>/policies — the Cedar policy that decides this tenant's
// requests, as the authorizer evaluates it (GetEffectivePolicy). The tenant's
// own layer is shown first; picking one of its collections adds the layers a
// request on that collection also meets — the bucket it is bound to, and the
// collection's own. Editing stays on /policies.

import { useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";

import { collectionClient, policyClient } from "@/lib/connect/client";
import { API_PAGE_SIZE_MAX } from "@/constants";
import { errorMessage } from "@/hooks/errorContract";
import { PolicyLayers } from "@/components/features/policy/PolicyLayers";
import { Card } from "@/components/ui/Card";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/Select";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "../tenant-context";

export default function TenantPoliciesPage() {
  const tenant = useTenant();
  const tenantName = `tenants/${tenant.tenantId}`;
  // The picker's value is the resource name the policy is read for: the
  // tenant's own, or one of its collections'.
  const [picked, setPicked] = useState("");

  const collections = useQuery({
    queryKey: ["tenant-policy-collections", tenant.tenantId],
    queryFn: async ({ signal }) =>
      (
        await collectionClient.listCollections(
          {
            parent: tenantName,
            page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
          },
          { signal },
        )
      ).collections,
  });

  // A pick from another tenant's page (the route changed under this one)
  // does not carry over.
  const resourceName = picked.startsWith(`${tenantName}/`)
    ? picked
    : tenantName;
  const scopes = [
    { value: tenantName, label: "The tenant" },
    ...(collections.data ?? []).map((c) => ({
      value: `${tenantName}/collections/${c.collection}`,
      label: `Collection ${c.collection}`,
    })),
  ];
  const effective = useQuery({
    queryKey: ["effective-policy", resourceName],
    queryFn: ({ signal }) =>
      policyClient.getEffectivePolicy({ resourceName }, { signal }),
  });

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold">Effective Cedar policy</h2>
        <p className={cn(T.helper, "max-w-prose")}>
          The layers the authorizer evaluates for{" "}
          <span className="font-mono">{tenant.displayName}</span>, in order.
          Edit, validate and simulate them at{" "}
          <Link href="/policies" className="text-primary hover:underline">
            /policies
          </Link>
          .
        </p>
      </div>

      <Card className="space-y-3 p-4">
        <div className="space-y-1.5">
          <Label htmlFor="policy-scope" className="text-xs">
            Scope
          </Label>
          <Select
            id="policy-scope"
            aria-label="Scope"
            options={scopes}
            value={resourceName}
            onChange={setPicked}
            className="w-full max-w-md"
          />
          <p className={T.hint}>
            A collection adds its bucket&apos;s layer and its own.
          </p>
        </div>

        {effective.isPending ? (
          <Skeleton className="h-40 w-full" />
        ) : effective.isError ? (
          <p role="alert" className="text-sm text-destructive">
            {errorMessage(effective.error, "Failed to load the policy")}
          </p>
        ) : (
          <PolicyLayers layers={effective.data.layers} />
        )}
      </Card>
    </div>
  );
}
