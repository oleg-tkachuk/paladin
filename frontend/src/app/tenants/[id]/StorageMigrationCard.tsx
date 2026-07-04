"use client";

import { useEffect, useState } from "react";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { useTenants } from "@/hooks/useTenants";
import type { StorageMigrationStatus } from "@/gen/paladin/admin/v1/tenant_service_pb";

// Terminal states — the copy job is done, so we stop polling.
const TERMINAL = new Set(["completed", "cleaned", "failed"]);

const STATE_VARIANT: Record<string, "default" | "secondary" | "destructive"> = {
  failed: "destructive",
  completed: "default",
  cleaned: "default",
};

// StorageMigrationCard surfaces a tenant's shared->dedicated storage migration
// (ADR-0011 Phase 3). It renders nothing when the tenant never migrated
// (GetTenantStorageMigration -> NOT_FOUND), and polls while a migration is
// in flight so progress updates live.
export function StorageMigrationCard({ tenantId }: { tenantId: string }) {
  const { getTenantStorageMigration } = useTenants();
  const [mig, setMig] = useState<StorageMigrationStatus | null>(null);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const poll = async () => {
      try {
        const m = await getTenantStorageMigration(tenantId);
        if (cancelled) return;
        setMig(m);
        setLoaded(true);
        if (m && !TERMINAL.has(m.state)) {
          timer = setTimeout(poll, 4000);
        }
      } catch {
        if (!cancelled) setLoaded(true);
      }
    };

    void poll();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [tenantId, getTenantStorageMigration]);

  // Wait until the first fetch resolves; render nothing when there is no migration.
  if (!loaded || !mig) return null;

  const total = Number(mig.objectsTotal);
  const copied = Number(mig.objectsCopied);
  const pct =
    total > 0
      ? Math.min(100, (copied / total) * 100)
      : TERMINAL.has(mig.state)
        ? 100
        : 0;
  const variant = STATE_VARIANT[mig.state] ?? "secondary";

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          Storage migration
          <Badge variant={variant}>{mig.state}</Badge>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        <div className="text-muted-foreground">
          {mig.sourceBackendId}/{mig.sourceBucketName}
          {" → "}
          {mig.targetBackendId}/{mig.targetBucketName}
        </div>
        {total > 0 && (
          <div className="space-y-1">
            <div className="h-2 w-full overflow-hidden rounded bg-secondary">
              <div
                className="h-full bg-primary transition-all"
                style={{ width: `${pct}%` }}
              />
            </div>
            <div className="text-xs text-muted-foreground">
              {copied} / {total} objects copied
            </div>
          </div>
        )}
        {mig.error && (
          <div className="text-xs text-destructive">{mig.error}</div>
        )}
      </CardContent>
    </Card>
  );
}
