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
//   - A cap that is set is a limit, where 0 allows nothing; an absent cap
//     is none, rendered `∞ unlimited` (_caps.ts).

import { useTenantChangesBlocked } from "../tenant-context";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
  ScaleIcon,
} from "@heroicons/react/24/outline";

import { Card, CardTitle, CardHeader, CardContent } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { LimitInput } from "@/components/ui/LimitInput";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { useNotification } from "@/components/ui/Notification";
import { quotaClient } from "@/lib/connect/client";
import { notFoundIsAnswer } from "@/lib/connect/expected";
import { QuotaSchema } from "@/gen/paladin/admin/v1/types_pb";
import { cn, formatBytes } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { isAbortError, errorMessage } from "@/hooks/errorContract";

import { useTenant } from "../tenant-context";
import { formatCount } from "@/lib/format/locale";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { fieldMask } from "@/lib/connect/fieldMask";
import { capFieldOf, capOf, capPercent, type CapField } from "./_caps";

/** A cap with no limit: what a tenant with no quota starts from. */
const NO_CAP: CapField = { value: "", unlimited: true };

function fmtCap(cap: bigint | undefined, fmt: (b: number) => string): string {
  return cap === undefined ? "∞ unlimited" : fmt(Number(cap));
}

function progressColour(pct: number): string {
  if (pct >= 90) return "bg-destructive";
  if (pct >= 70) return "bg-warning";
  return "bg-success";
}

export default function TenantQuotasPage() {
  const changesBlocked = useTenantChangesBlocked();
  const tenant = useTenant();
  const quotaName = `tenants/${tenant.tenantId}/quota`;
  const { showNotification } = useNotification();

  // Form state: each cap's typed limit and whether it is lifted. A tenant
  // with no quota starts with every cap Unlimited. Hydrated from the latest
  // snapshot.
  const [maxTotalBytes, setMaxTotalBytes] = useState(NO_CAP);
  const [maxObjectCount, setMaxObjectCount] = useState(NO_CAP);
  const [maxBytesPerDay, setMaxBytesPerDay] = useState(NO_CAP);
  const [maxObjectsPerDay, setMaxObjectsPerDay] = useState(NO_CAP);
  const [submitting, setSubmitting] = useState(false);
  const [resetting, setResetting] = useState(false);

  const quotaQuery = useQuery({
    queryKey: ["tenantQuota", quotaName],
    retry: false, // queryFn toasts real failures; NotFound is a normal state.
    queryFn: async ({ signal }) => {
      try {
        const res = await quotaClient.getQuota(
          { name: quotaName },
          notFoundIsAnswer({ signal }),
        );
        return { quota: res, notFound: false };
      } catch (err) {
        // An aborted query is not a failure the operator needs to see:
        // TanStack cancels in-flight reads on unmount and on supersede.
        if (isAbortError(err)) throw err;
        // No quota row yet is a normal "unlimited / create" state, not an error.
        if (err instanceof ConnectError && err.code === Code.NotFound) {
          return { quota: null, notFound: true };
        }
        showNotification({
          type: "error",
          title: "Load failed",
          message: errorMessage(err, "Failed to load quota"),
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
      setMaxTotalBytes(capFieldOf(quota.maxTotalBytes));
      setMaxObjectCount(capFieldOf(quota.maxObjectCount));
      setMaxBytesPerDay(capFieldOf(quota.maxBytesPerDay));
      setMaxObjectsPerDay(capFieldOf(quota.maxObjectsPerDay));
    } else {
      setMaxTotalBytes(NO_CAP);
      setMaxObjectCount(NO_CAP);
      setMaxBytesPerDay(NO_CAP);
      setMaxObjectsPerDay(NO_CAP);
    }
  }

  const caps = {
    maxTotalBytes: capOf(maxTotalBytes),
    maxObjectCount: capOf(maxObjectCount),
    maxBytesPerDay: capOf(maxBytesPerDay),
    maxObjectsPerDay: capOf(maxObjectsPerDay),
  };
  // A cap with Unlimited off and no whole number typed is held, not sent.
  const capsInvalid = Object.values(caps).some((c) => c === null);

  const handleSubmit = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (capsInvalid) return;
    setSubmitting(true);
    try {
      const next = create(QuotaSchema, {
        name: quotaName,
        maxTotalBytes: caps.maxTotalBytes ?? undefined,
        maxObjectCount: caps.maxObjectCount ?? undefined,
        maxBytesPerDay: caps.maxBytesPerDay ?? undefined,
        maxObjectsPerDay: caps.maxObjectsPerDay ?? undefined,
      });
      // FieldMask covers all four caps — usage stays untouched
      // (server-managed). A masked cap the quota leaves absent is lifted,
      // which is how Unlimited is sent. resource_version is the OCC guard and is now
      // required: "0" asserts "no quota row exists yet", which is what the
      // first save means. Sending "" would be rejected by validation, and
      // sending a stale version is Aborted rather than silently overwriting
      // another operator's limits.
      await quotaClient.setQuota({
        name: quotaName,
        resourceVersion: quota?.resourceVersion || "0",
        updateMask: fieldMask(
          QuotaSchema,
          "maxTotalBytes",
          "maxObjectCount",
          "maxBytesPerDay",
          "maxObjectsPerDay",
        ),
        quota: next,
      });
      await fetchQuota();
      showNotification({ type: "success", title: "Quota updated" });
    } catch (err) {
      const msg = errorMessage(err, "Update failed");
      showNotification({ type: "error", title: "Update failed", message: msg });
    } finally {
      setSubmitting(false);
    }
  };

  // Zeroing today's counters lets the tenant spend its daily allowance again,
  // and the usage it had is not kept anywhere. It was one click.
  const [confirmReset, setConfirmReset] = useState(false);

  const handleResetUsage = async () => {
    setResetting(true);
    try {
      await quotaClient.resetUsage({ name: quotaName });
      showNotification({ type: "success", title: "Usage counter reset" });
      void fetchQuota();
    } catch (err) {
      const msg = errorMessage(err, "Reset failed");
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
            presign time; a cap of 0 allows nothing.
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
        ) : quotaQuery.isError && !quotaQuery.data ? (
          // A failed read rendered an empty card, and Reset usage disabled
          // as if no quota existed.
          <ListLoadError
            what="Quota"
            reason={errorMessage(quotaQuery.error)}
            onRetry={() => void quotaQuery.refetch()}
          />
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
              fmt={formatCount}
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
              fmt={formatCount}
            />
          </div>
        ) : null}
      </Card>

      {/* Set form */}
      <Card>
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <CardHeader>
            <CardTitle
              level={3}
              className="text-base font-medium"
              icon={ScaleIcon}
            >
              Update caps
            </CardTitle>
            <p className="text-sm text-muted-foreground">
              Enter raw integers (no SI suffixes). 0 allows nothing; Unlimited
              sets no cap.
            </p>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <CapInput
                id="max-total-bytes"
                label="Max total bytes"
                hint={`Hard cap on storage. ${fmtCap(caps.maxTotalBytes ?? undefined, formatBytes)}`}
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
                hint={`Daily upload byte budget. ${fmtCap(caps.maxBytesPerDay ?? undefined, formatBytes)}`}
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
                disabled={Boolean(changesBlocked) || resetting || !quota}
                title={changesBlocked ?? undefined}
                onClick={() => setConfirmReset(true)}
              >
                {resetting ? "Resetting…" : "Reset daily counters"}
              </Button>
              {/* Disabled while the quota is still loading, not just while
                submitting. The form's resource_version comes from the fetched
                quota; before it arrives the state is null and the request
                would carry "0" — which asserts "no quota row exists" and is
                refused with a version mismatch if one does. The user sees
                "Update failed" for a click that was simply too early. */}
              <Button
                type="submit"
                disabled={
                  Boolean(changesBlocked) ||
                  submitting ||
                  loading ||
                  capsInvalid
                }
                title={changesBlocked ?? undefined}
              >
                {submitting ? (
                  "Updating…"
                ) : loading ? (
                  "Loading…"
                ) : (
                  <>
                    <CheckCircleIcon className="size-4" />
                    {notFound ? "Create quota" : "Apply changes"}
                  </>
                )}
              </Button>
            </div>
          </CardContent>
        </form>
      </Card>
      <ConfirmModal
        isOpen={confirmReset}
        onClose={() => setConfirmReset(false)}
        onConfirm={handleResetUsage}
        title="Reset today's counters?"
        message="The tenant can use its full daily allowance again. Today's usage so far is discarded."
        confirmText="Reset counters"
        loading={resetting}
      />
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
  cap: bigint | undefined;
  used: bigint;
  fmt: (n: number) => string;
}) {
  const capped = cap !== undefined;
  const pct = capped ? capPercent(used, cap) : 0;
  const overCap = capped && used >= cap;
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between">
        <Label className="text-xs text-muted-foreground">{label}</Label>
        {overCap && (
          <Badge variant="destructive">
            <ExclamationTriangleIcon className="mr-1 size-3" />
            cap reached
          </Badge>
        )}
      </div>
      <div className="font-mono text-sm">
        {fmt(Number(used))}{" "}
        <span className="text-muted-foreground">/ {fmtCap(cap, fmt)}</span>
      </div>
      {capped && (
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
  value: CapField;
  onChange: (v: CapField) => void;
}) {
  const invalid = capOf(value) === null && value.value.trim() !== "";
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <LimitInput
        id={id}
        value={value.value}
        onChange={(v) => onChange({ ...value, value: v })}
        unlimited={value.unlimited}
        onUnlimitedChange={(next) => onChange({ ...value, unlimited: next })}
        placeholder="0 allows nothing"
        aria-invalid={invalid || undefined}
      />
      <p className={T.hint}>
        {invalid ? "A whole number, or Unlimited." : hint}
      </p>
    </div>
  );
}
