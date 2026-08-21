"use client";

import { useCallback, useEffect, useState } from "react";
import { parseBucketResourceName } from "@/lib/resources/bucket-name";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
// (ADR-0011 Phase 3):
//   - a migration in flight / completed → live status + progress;
//   - a `shared` tenant with no migration → a trigger to start one;
//   - a `dedicated` tenant with no migration → nothing (the identity card's
//     layout badge already says it's dedicated).
/** "storageBackends/x/buckets/y" → "x/y" for the migration summary line;
 *  anything that does not parse is shown as sent. */
function shortBucket(name: string): string {
  const ref = parseBucketResourceName(name);
  return ref ? `${ref.backendId}/${ref.bucketId}` : name;
}

export function StorageMigrationCard({
  tenantId,
  storageLayout,
}: {
  tenantId: string;
  storageLayout: string;
}) {
  const { getTenantStorageMigration, migrateTenantStorageLayout } =
    useTenants();
  const [mig, setMig] = useState<StorageMigrationStatus | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);
  // Retention (hours) for the old shared copies after the migration completes;
  // blank uses the server default (24h).
  const [retentionHours, setRetentionHours] = useState("");

  const poll = useCallback(async () => {
    const m = await getTenantStorageMigration(tenantId);
    setMig(m);
    setLoaded(true);
    return m;
  }, [getTenantStorageMigration, tenantId]);

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const loop = async () => {
      try {
        const m = await poll();
        if (cancelled) return;
        if (m && !TERMINAL.has(m.state)) {
          timer = setTimeout(loop, 4000);
        }
      } catch {
        if (!cancelled) setLoaded(true);
      }
    };

    void loop();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [poll]);

  const start = useCallback(async () => {
    setStarting(true);
    setStartError(null);
    try {
      const hours = retentionHours.trim();
      const created = await migrateTenantStorageLayout(tenantId, {
        cleanupRetentionSeconds: hours ? Number(hours) * 3600 : undefined,
      });
      setMig(created); // polling resumes via the effect (mig now non-terminal)
    } catch (err) {
      setStartError(
        err instanceof Error ? err.message : "Migration failed to start",
      );
    } finally {
      setStarting(false);
    }
  }, [migrateTenantStorageLayout, retentionHours, tenantId]);

  if (!loaded) return null;

  // No migration yet.
  if (!mig) {
    // Only a shared tenant can migrate; a dedicated tenant with no migration
    // has nothing to show here.
    if (storageLayout !== "shared") return null;
    return (
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm font-medium">
            Storage migration
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <p className="text-muted-foreground">
            This tenant is on the <strong>shared</strong> bucket. Migrating to a
            dedicated bucket copies its objects over, rebinds them atomically,
            then keeps the old copies for a retention window before cleanup.
          </p>
          <div className="flex items-end gap-2">
            <label className="space-y-1">
              <span className="text-xs text-muted-foreground">
                Cleanup retention (hours, blank = 24h default)
              </span>
              <Input
                type="number"
                min={0}
                inputMode="numeric"
                placeholder="24"
                value={retentionHours}
                onChange={(e) => setRetentionHours(e.target.value)}
                className="w-40"
              />
            </label>
            <Button onClick={start} disabled={starting}>
              {starting ? "Starting…" : "Migrate to dedicated"}
            </Button>
          </div>
          {startError && (
            <div className="text-xs text-destructive">{startError}</div>
          )}
        </CardContent>
      </Card>
    );
  }

  // Migration in flight / done.
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
          {shortBucket(mig.sourceBucket)}
          {" → "}
          {shortBucket(mig.targetBucket)}
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
