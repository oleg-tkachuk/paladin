"use client";

import { IconTone } from "@/components/ui/IconChip";
import { ArrowsRightLeftIcon } from "@heroicons/react/24/outline";
import { useTenantChangesBlocked } from "./tenant-context";
import { useCallback, useEffect, useState } from "react";
import { parseBucketResourceName } from "@/lib/resources/bucket-name";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useTenants } from "@/hooks/useTenants";
import type { StorageMigrationStatus } from "@/gen/paladin/admin/v1/tenant_service_pb";
import { STORAGE_LAYOUT_SHARED } from "@/lib/storageLayout";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import {
  MIGRATION_CLEANED,
  MIGRATION_COMPLETED,
  MIGRATION_FAILED,
  MIGRATION_ROLLING_BACK,
  canCancelMigration,
  migrationCancelWarning,
  migrationInFlight,
} from "@/lib/storageMigration";

/** How often a migration in flight is read again. */
const POLL_INTERVAL_MS = 4000;

const STATE_VARIANT: Record<string, "default" | "secondary" | "destructive"> = {
  [MIGRATION_FAILED]: "destructive",
  [MIGRATION_ROLLING_BACK]: "destructive",
  [MIGRATION_COMPLETED]: "default",
  [MIGRATION_CLEANED]: "default",
};

// StorageMigrationCard surfaces a tenant's shared->dedicated storage migration
// (ADR-0015 Phase 3):
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
  const changesBlocked = useTenantChangesBlocked();
  const {
    getTenantStorageMigration,
    migrateTenantStorageLayout,
    cancelTenantStorageMigration,
  } = useTenants();
  const [mig, setMig] = useState<StorageMigrationStatus | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);
  // Retention (hours) for the old shared copies after the migration completes;
  // blank uses the server default (24h).
  const [retentionHours, setRetentionHours] = useState("");
  const [confirmCancel, setConfirmCancel] = useState(false);
  // Bumped by a start or a cancel, which leave the migration in flight
  // again: the polling loop stopped at the settled state read before them.
  const [pollGeneration, setPollGeneration] = useState(0);

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
        if (m && migrationInFlight(m.state)) {
          timer = setTimeout(loop, POLL_INTERVAL_MS);
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
  }, [poll, pollGeneration]);

  const start = useCallback(async () => {
    setStarting(true);
    setStartError(null);
    try {
      const hours = retentionHours.trim();
      const created = await migrateTenantStorageLayout(tenantId, {
        cleanupRetentionSeconds: hours ? Number(hours) * 3600 : undefined,
      });
      setMig(created);
      setPollGeneration((g) => g + 1);
    } catch (err) {
      setStartError(
        err instanceof Error ? err.message : "Migration failed to start",
      );
    } finally {
      setStarting(false);
    }
  }, [migrateTenantStorageLayout, retentionHours, tenantId]);

  // Throws on a refusal, which the confirmation shows; it closes on success.
  const cancel = useCallback(async () => {
    if (!mig) return;
    const cancelled = await cancelTenantStorageMigration(
      tenantId,
      mig.resourceVersion,
    );
    setMig(cancelled);
    setPollGeneration((g) => g + 1);
  }, [cancelTenantStorageMigration, mig, tenantId]);

  if (!loaded) return null;

  // No migration yet.
  if (!mig) {
    // Only a shared tenant can migrate; a dedicated tenant with no migration
    // has nothing to show here.
    if (storageLayout !== STORAGE_LAYOUT_SHARED) return null;
    return (
      <Card>
        <CardHeader className="pb-2">
          <CardTitle
            className="text-sm font-medium"
            icon={ArrowsRightLeftIcon}
            tone={IconTone.Info}
          >
            Storage migration
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <p className="text-muted-foreground">
            This tenant is on the <strong>shared</strong> bucket. Migrating to a
            dedicated bucket sends new uploads there and moves its objects over
            one by one, then keeps the old copies for a retention window before
            cleanup. It can be cancelled until the cleanup.
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
            <Button
              onClick={start}
              disabled={Boolean(changesBlocked) || starting}
              title={changesBlocked ?? undefined}
            >
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
      : migrationInFlight(mig.state)
        ? 0
        : 100;
  const variant = STATE_VARIANT[mig.state] ?? "secondary";

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle
          className="text-sm font-medium"
          icon={ArrowsRightLeftIcon}
          tone={IconTone.Info}
        >
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
        {mig.state === MIGRATION_ROLLING_BACK && (
          <div className="text-xs text-muted-foreground">
            The tenant is being moved back to its source bucket; the migration
            can be retried once it has failed.
          </div>
        )}
        {mig.error && (
          <div className="text-xs text-destructive">{mig.error}</div>
        )}
        {canCancelMigration(mig.state) && (
          <Button
            variant="outline"
            size="sm"
            onClick={() => setConfirmCancel(true)}
            disabled={Boolean(changesBlocked)}
            title={changesBlocked ?? undefined}
          >
            Cancel migration
          </Button>
        )}
      </CardContent>
      <ConfirmModal
        isOpen={confirmCancel}
        onClose={() => setConfirmCancel(false)}
        onConfirm={cancel}
        type="warning"
        title="Cancel the storage migration?"
        message={`The migration is ${mig.state}.`}
        warning={migrationCancelWarning(mig.state)}
        confirmText="Cancel migration"
        cancelText="Keep migrating"
      />
    </Card>
  );
}
