"use client";

// /tenants/<id>/object-keys — placeholder for Phase 3.
// Same pattern as the Buckets stub: link out to the legacy flat
// list while the real tenant-scoped table is wired up.

import Link from "next/link";

import { Card, CardContent } from "@/components/ui/Card";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "../tenant-context";

export default function TenantObjectKeysPage() {
  const tenant = useTenant();
  return (
    <Card>
      <CardContent className="space-y-2 p-6">
        <p className="text-sm font-medium">Object Keys — under refactor</p>
        <p className={cn(T.helper, "max-w-prose")}>
          The tenant-scoped object-keys list lands in Phase 3 alongside the move
          of <span className={T.code}>/object-keys/&lt;name&gt;</span> →{" "}
          <span className={T.code}>
            /tenants/{tenant.slug}/object-keys/&lt;name&gt;
          </span>
          .
        </p>
        <p>
          <Link
            href="/object-keys"
            className="text-sm font-medium text-primary hover:underline"
          >
            → Open /object-keys (cross-tenant index)
          </Link>
        </p>
      </CardContent>
    </Card>
  );
}
