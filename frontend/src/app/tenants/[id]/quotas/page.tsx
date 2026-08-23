"use client";

// Tenant Quotas tab — written from scratch (no legacy /quotas
// page existed). Reads `tenants/<id>/quota` via QuotaService.GetQuota
// and renders the four hard caps (total bytes, object count, daily
// byte budget, daily object budget) plus current usage. Set form
// pushes changes via SetQuota with an OCC guard.
//
// Usage row is read-only: it's maintained by the accounting worker;
// `Reset daily counters` calls QuotaService.ResetUsage as an ops
// escape hatch (ordinarily the worker handles it at period
// boundaries).
//
// Notes:
//   - Quota name format: `tenants/{tenant_id_or_slug}/quota`. Bucket
//     quotas live under storageBackends/.../buckets/.../quota and
//     surface in the per-bucket Quota tab (BACKLOG; not in this slice).
//   - 0 caps mean "unlimited" per proto contract — same convention as
//     TenantBudget.maxBudgetAmount. UI renders `∞ unlimited`.

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
  ScaleIcon,
} from "@heroicons/react/24/outline";

import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { useNotification } from "@/components/ui/Notification";
import { quotaClient } from "@/lib/connect/client";
import { QuotaSchema } from "@/gen/paladin/admin/v1/types_pb";
import { cn, formatBytes } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "../tenant-context";

function bigIntFromInput(s: string): bigint {
  const n = s.trim();
  if (!n) return 0n;
  try {
    return BigInt(n);
  } catch {
    return 0n;
  }
}

function fmtCap(n: bigint, fmt: (b: number) => string): string {
  if (n === 0n) return "∞ unlimited";
  return fmt(Number(n));
}

function pctOf(used: bigint, cap: bigint): number {
  if (cap <= 0n) return 0;
  return Math.min(100, (Number(used) / Number(cap)) * 100);
}

function progressColour(pct: number): string {
  if (pct >= 90) return "bg-destructive";
  if (pct >= 70) return "bg-amber-500";
  return "bg-emerald-500";
}

export default function TenantQuotasPage() {
  const tenant = useTenant();
  const quotaName = `tenants/${tenant.tenantId}/quota`;
  const { showNotification } = useNotification();

  // Form state mirrors the four caps as strings (so an empty input
  // round-trips to 0 unlimited). Hydrated from the latest snapshot.
  const [maxTotalBytes, setMaxTotalBytes] = useState("");
  const [maxObjectCount, setMaxObjectCount] = useState("");
  const [maxBytesPerDay, setMaxBytesPerDay] = useState("");
  const [maxObjectsPerDay, setMaxObjectsPerDay] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [resetting, setResetting] = useState(false);

  const quotaQuery = useQuery({
    queryKey: ["tenantQuota", quotaName],
    retry: false, // queryFn toasts real failures; NotFound is a normal state.
    queryFn: async ({ signal }) => {
      try {
        const res = await quotaClient.getQuota({ name: quotaName }, { signal });
        return { quota: res, notFound: false };
      } catch (err) {
        // No quota row yet is a normal "unlimited / create" state, not an error.
        if (err instanceof ConnectError && err.code === Code.NotFound) {
          return { quota: null, notFound: true };
        }
        showNotification({
          type: "error",
          title: "Load failed",
          message:
            err instanceof ConnectError
              ? err.rawMessage
              : "Failed to load quota",
        });
        throw err;
      }
    },
  });
  const quota = quotaQuery.data?.quota ?? null;
  const notFound = quotaQuery.data?.notFound ?? false;
  const loading = quotaQuery.isFetching;
  const fetchQuota = () => quotaQuery.refetch();

  // Hydrate the form whenever a new snapshot arrives — render-phase
  // adjust-on-change (React's recommended alternative to a sync effect, not a
  // set-state-in-effect hit). `quota` identity changes only on real data
  // change (TanStack structural sharing).
  const [seededFrom, setSeededFrom] = useState(quota);
  if (quota !== seededFrom) {
    setSeededFrom(quota);
    if (quota) {
      setMaxTotalBytes(String(quota.maxTotalBytes));
      setMaxObjectCount(String(quota.maxObjectCount));
      setMaxBytesPerDay(String(quota.maxBytesPerDay));
      setMaxObjectsPerDay(String(quota.maxObjectsPerDay));
    } else {
      setMaxTotalBytes("");
      setMaxObjectCount("");
      setMaxBytesPerDay("");
      setMaxObjectsPerDay("");
    }
  }

  const handleSubmit = async (e?: React.FormEvent) => {
    e?.preventDefault();
    setSubmitting(true);
    try {
      const next = create(QuotaSchema, {
        name: quotaName,
        maxTotalBytes: bigIntFromInput(maxTotalBytes),
        maxObjectCount: bigIntFromInput(maxObjectCount),
        maxBytesPerDay: bigIntFromInput(maxBytesPerDay),
        maxObjectsPerDay: bigIntFromInput(maxObjectsPerDay),
      });
      // FieldMask covers all four caps — usage stays untouched
      // (server-managed). resource_version is the OCC guard and is now
      // required: "0" asserts "no quota row exists yet", which is what the
      // first save means. Sending "" would be rejected by validation, and
      // sending a stale version is Aborted rather than silently overwriting
      // another operator's limits.
      await quotaClient.setQuota({
        name: quotaName,
        resourceVersion: quota?.resourceVersion || "0",
        updateMask: create(FieldMaskSchema, {
          paths: [
            "max_total_bytes",
            "max_object_count",
            "max_bytes_per_day",
            "max_objects_per_day",
          ],
        }),
        quota: next,
      });
      await fetchQuota();
      showNotification({ type: "success", title: "Quota updated" });
    } catch (err) {
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Update failed";
      showNotification({ type: "error", title: "Update failed", message: msg });
    } finally {
      setSubmitting(false);
    }
  };

  const handleResetUsage = async () => {
    setResetting(true);
    try {
      await quotaClient.resetUsage({ name: quotaName });
      showNotification({ type: "success", title: "Usage counter reset" });
      void fetchQuota();
    } catch (err) {
      const msg = err instanceof ConnectError ? err.rawMessage : "Reset failed";
      showNotification({ type: "error", title: "Reset failed", message: msg });
    } finally {
      setResetting(false);
    }
  };

  const usage = quota?.usage;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">Quotas</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            Hard caps on storage and request volume for{" "}
            <span className="font-mono">{tenant.displayName}</span>. Enforced at
            presign time; 0 means no cap.
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => void fetchQuota()}
          disabled={loading}
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
          Refresh
        </Button>
      </div>

      {/* Snapshot */}
      <Card className="p-6">
        {loading && !quota ? (
          <div className="space-y-3">
            <Skeleton className="h-6 w-1/3" />
            <Skeleton className="h-4 w-1/2" />
            <Skeleton className="h-3 w-full" />
          </div>
        ) : notFound ? (
          <div className="flex items-start gap-3 text-sm">
            <ScaleIcon className="mt-0.5 size-5 text-muted-foreground" />
            <div>
              <p className="font-medium">No quota configured.</p>
              <p className="text-muted-foreground">
                Set caps below to activate enforcement; while unset, presign
                operations succeed unconditionally.
              </p>
            </div>
          </div>
        ) : quota ? (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <UsageRow
              label="Total bytes"
              cap={quota.maxTotalBytes}
              used={usage?.totalBytes ?? 0n}
              fmt={(n) => formatBytes(n)}
            />
            <UsageRow
              label="Object count"
              cap={quota.maxObjectCount}
              used={usage?.objectCount ?? 0n}
              fmt={(n) => n.toLocaleString()}
            />
            <UsageRow
              label="Bytes today"
              cap={quota.maxBytesPerDay}
              used={usage?.bytesToday ?? 0n}
              fmt={(n) => formatBytes(n)}
            />
            <UsageRow
              label="Objects today"
              cap={quota.maxObjectsPerDay}
              used={usage?.objectsToday ?? 0n}
              fmt={(n) => n.toLocaleString()}
            />
          </div>
        ) : null}
      </Card>

      {/* Set form */}
      <Card className="p-6">
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <h3 className="text-base font-medium">Update caps</h3>
            <p className="text-sm text-muted-foreground">
              Enter raw integers (no SI suffixes). 0 = unlimited.
            </p>
          </div>

          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <CapInput
              id="max-total-bytes"
              label="Max total bytes"
              hint={`Hard cap on storage. ${formatBytes(bigIntFromInput(maxTotalBytes) ? Number(bigIntFromInput(maxTotalBytes)) : 0)}`}
              value={maxTotalBytes}
              onChange={setMaxTotalBytes}
            />
            <CapInput
              id="max-object-count"
              label="Max object count"
              hint="Hard cap on number of live objects."
              value={maxObjectCount}
              onChange={setMaxObjectCount}
            />
            <CapInput
              id="max-bytes-per-day"
              label="Max bytes / day"
              hint={`Daily upload byte budget. ${formatBytes(bigIntFromInput(maxBytesPerDay) ? Number(bigIntFromInput(maxBytesPerDay)) : 0)}`}
              value={maxBytesPerDay}
              onChange={setMaxBytesPerDay}
            />
            <CapInput
              id="max-objects-per-day"
              label="Max objects / day"
              hint="Daily upload object-count budget."
              value={maxObjectsPerDay}
              onChange={setMaxObjectsPerDay}
            />
          </div>

          <div className="flex items-center justify-between">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={resetting || !quota}
              onClick={() => void handleResetUsage()}
            >
              {resetting ? "Resetting…" : "Reset daily counters"}
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? (
                "Updating…"
              ) : (
                <>
                  <CheckCircleIcon className="size-4" />
                  {notFound ? "Create quota" : "Apply changes"}
                </>
              )}
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}

function UsageRow({
  label,
  cap,
  used,
  fmt,
}: {
  label: string;
  cap: bigint;
  used: bigint;
  fmt: (n: number) => string;
}) {
  const pct = pctOf(used, cap);
  const overCap = cap > 0n && used >= cap;
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between">
        <Label className="text-xs text-muted-foreground">{label}</Label>
        {overCap && (
          <Badge variant="destructive" className={T.labelTight}>
            <ExclamationTriangleIcon className="mr-1 size-3" />
            cap reached
          </Badge>
        )}
      </div>
      <div className="font-mono text-sm">
        {fmt(Number(used))}{" "}
        <span className="text-muted-foreground">/ {fmtCap(cap, fmt)}</span>
      </div>
      {cap > 0n && (
        <div className="h-1.5 w-full overflow-hidden rounded bg-muted">
          <div
            className={cn("h-full transition-all", progressColour(pct))}
            style={{ width: `${pct}%` }}
          />
        </div>
      )}
    </div>
  );
}

function CapInput({
  id,
  label,
  hint,
  value,
  onChange,
}: {
  id: string;
  label: string;
  hint: string;
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="number"
        min={0}
        placeholder="0 = unlimited"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      <p className={T.hint}>{hint}</p>
    </div>
  );
}
