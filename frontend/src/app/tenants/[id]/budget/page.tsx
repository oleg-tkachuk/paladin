"use client";

// Tenant Budget tab — pulled out of the legacy /tenant-budgets
// page (deleted in this commit). Snapshot + Set form for the
// per-tenant aggregate spend cap that backs the capability
// subsystem's two-phase Charge.
//
// Differences from the legacy page:
//   - tenantId comes from useTenant() (URL-bound) instead of
//     useScope() (auth-bound), so platform-admins can edit any
//     tenant's budget by navigating into it.
//   - No PageHeader: the parent TenantLayout already drew the
//     page chrome. We render a focused header band like the Collection /
//     Bucket detail layouts.
//   - No "Pick a tenant from the top bar" empty-state — the URL
//     pins the tenant.
//
// Behaviour parity with /tenant-budgets is otherwise verbatim:
// NOT_FOUND → "no budget configured" + Set form to create the
// first one; non-zero cap activates server-side enforcement.

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowPathIcon,
  BanknotesIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";
import { ConnectError, Code } from "@connectrpc/connect";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { useNotification } from "@/components/ui/Notification";
import { tenantBudgetClient } from "@/lib/connect/client";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { formatMoney, ALLOWED_UNIT_CODES } from "@/lib/format/money";
import { Select } from "@/components/ui/Select";
import { isAbortError } from "@/hooks/errorContract";

import { useTenant } from "../tenant-context";

function formatTimestamp(ts: { seconds: bigint } | undefined): string {
  if (!ts) return "—";
  const ms = Number(ts.seconds) * 1000;
  if (!ms) return "—";
  try {
    return new Date(ms).toISOString().replace("T", " ").replace(".000Z", "Z");
  } catch {
    return "—";
  }
}

function formatAmount(n: number, unit: string): string {
  return formatMoney(n, unit);
}

function progressColour(spent: number, max: number): string {
  if (max <= 0) return "bg-primary/40";
  const pct = (spent / max) * 100;
  if (pct >= 90) return "bg-destructive";
  if (pct >= 70) return "bg-amber-500";
  return "bg-emerald-500";
}

export default function TenantBudgetPage() {
  const tenant = useTenant();
  const tenantId = tenant.tenantId;
  const { showNotification } = useNotification();

  const budgetQuery = useQuery({
    queryKey: ["tenantBudget", tenantId],
    retry: false, // queryFn toasts real failures; NotFound is a normal state.
    queryFn: async ({ signal }) => {
      try {
        const res = await tenantBudgetClient.get({ tenantId }, { signal });
        return { budget: res.budget ?? null, notFound: false };
      } catch (err) {
        // An aborted query is not a failure the operator needs to see:
        // TanStack cancels in-flight reads on unmount and on supersede.
        if (isAbortError(err)) throw err;
        // No budget row yet is a normal "Create budget" state, not an error.
        if (err instanceof ConnectError && err.code === Code.NotFound) {
          return { budget: null, notFound: true };
        }
        showNotification({
          type: "error",
          title: "Load failed",
          message:
            err instanceof ConnectError
              ? err.rawMessage
              : "Failed to load tenant budget",
        });
        throw err;
      }
    },
  });
  const budget = budgetQuery.data?.budget ?? null;
  const notFound = budgetQuery.data?.notFound ?? false;
  const loading = budgetQuery.isFetching;
  // Distinct from `loading`: true only until the first read resolves. Submit is
  // held on this, never on isFetching — a background refetch must not disable
  // Apply out from under an operator who is already clicking it.
  const initialising = budgetQuery.isPending;
  const fetchBudget = () => budgetQuery.refetch();

  const [maxBudget, setMaxBudget] = useState<string>("");
  // Form picker default. Cold-start (no existing budget) starts on
  // UNIT — the abstract metering sentinel — so the page doesn't
  // assume operators want USD. The hydration effect below replaces
  // this with budget.unitCode whenever a snapshot loads.
  const [unitCode, setUnitCode] = useState<string>("UNIT");
  const [resetSpend, setResetSpend] = useState<boolean>(false);
  // Period close date (YYYY-MM-DD, local). Blank leaves the server's window
  // untouched; a value pins when the billing period ends.
  const [periodEnd, setPeriodEnd] = useState<string>("");
  const [submitting, setSubmitting] = useState(false);
  // Set once the operator touches any field, cleared when their edit lands.
  // While set, an arriving snapshot is not written into the form.
  const [edited, setEdited] = useState(false);

  // Hydrate the form whenever a new snapshot arrives — render-phase
  // adjust-on-change (React's recommended alternative to a sync effect, and
  // not a set-state-in-effect hit). `budget` identity only changes on real
  // data change thanks to TanStack's structural sharing.
  //
  // Skipped while the operator has unsaved edits: a refetch — Refresh, a
  // remount, the read this page issues after a save — used to overwrite what
  // they had typed with the stored values, so a cap they had just entered
  // reverted mid-edit and Apply then submitted the old number. seededFrom is
  // left behind deliberately, so the pending snapshot seeds the form the
  // moment the edit resolves.
  const [seededFrom, setSeededFrom] = useState(budget);
  if (budget !== seededFrom && !edited) {
    setSeededFrom(budget);
    if (budget) {
      setMaxBudget(String(budget.maxBudgetAmount));
      if (budget.unitCode) setUnitCode(budget.unitCode);
      setPeriodEnd(
        budget.periodEnd
          ? new Date(Number(budget.periodEnd.seconds) * 1000)
              .toISOString()
              .slice(0, 10)
          : "",
      );
    } else {
      setMaxBudget("");
      setUnitCode("UNIT");
      setPeriodEnd("");
    }
    setResetSpend(false);
  }

  const handleSubmit = async (e?: React.FormEvent) => {
    e?.preventDefault();
    const cap = Number.parseFloat(maxBudget || "0");
    if (!Number.isFinite(cap) || cap < 0) {
      showNotification({
        type: "error",
        title: "Validation",
        message: "Max budget must be a non-negative number.",
      });
      return;
    }
    setSubmitting(true);
    try {
      await tenantBudgetClient.set({
        tenantId,
        maxBudgetAmount: cap,
        unitCode,
        resetSpend,
        // OCC guard. "0" asserts no row exists yet — the create case — and is
        // itself rejected if someone created one in the meantime. Anything
        // else is the version this page last read, so a concurrent edit by
        // another operator is refused instead of silently overwritten.
        resourceVersion: budget?.resourceVersion || "0",
        // Pin the period close date when set; blank leaves the server window.
        periodEnd: periodEnd
          ? timestampFromDate(new Date(`${periodEnd}T00:00:00Z`))
          : undefined,
      });
      setEdited(false);
      await fetchBudget();
      showNotification({
        type: "success",
        title: "Budget updated",
        message: resetSpend
          ? `Cap set to ${formatAmount(cap, unitCode)} and period rolled.`
          : `Cap set to ${formatAmount(cap, unitCode)}.`,
      });
    } catch (err) {
      // Aborted is the OCC guard, not a fault: the row moved under us. Refetch
      // so the form reflects what is actually stored before the operator
      // decides whether they still want their change.
      if (err instanceof ConnectError && err.code === Code.Aborted) {
        // The stored row wins: drop the edit flag so the refetched snapshot
        // seeds the form and the operator sees what actually landed.
        setEdited(false);
        await fetchBudget();
        showNotification({
          type: "error",
          title: "Budget changed elsewhere",
          message:
            "Someone else updated this budget while you were editing. " +
            "The form now shows the current values — re-apply if you still want your change.",
        });
        return;
      }
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Update failed";
      showNotification({ type: "error", title: "Update failed", message: msg });
    } finally {
      setSubmitting(false);
    }
  };

  const spent = Number(budget?.spentAmount ?? 0);
  const cap = Number(budget?.maxBudgetAmount ?? 0);
  // Display fallback when an existing budget row carries no
  // unit_code (legacy data minted before the field was wired).
  // UNIT is correct: rendering "$0.00" for what's actually
  // metering would lie about the configured currency.
  const budgetUnit = budget?.unitCode || "UNIT";
  const pct = cap > 0 ? Math.min(100, (spent / cap) * 100) : 0;
  const overCap = cap > 0 && spent >= cap;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">Budget</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            Aggregate spend cap for{" "}
            <span className="font-mono">{tenant.displayName}</span>. Capability
            charges accumulate against this row; the runtime rejects new charges
            once the cap is reached.
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => void fetchBudget()}
          disabled={loading}
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
          Refresh
        </Button>
      </div>

      {/* Snapshot */}
      <Card className="p-6">
        {loading && !budget ? (
          <div className="space-y-3">
            <Skeleton className="h-6 w-1/3" />
            <Skeleton className="h-4 w-1/2" />
            <Skeleton className="h-3 w-full" />
          </div>
        ) : notFound ? (
          <div className="flex items-start gap-3 text-sm">
            <BanknotesIcon className="mt-0.5 size-5 text-muted-foreground" />
            <div>
              <p className="font-medium">No budget configured.</p>
              <p className="text-muted-foreground">
                Set a non-zero cap below to activate server-side enforcement.
                While zero, capability charges still accumulate but never
                reject.
              </p>
            </div>
          </div>
        ) : budget ? (
          <div className="space-y-4">
            <div className="flex flex-wrap items-baseline gap-x-6 gap-y-2">
              <div>
                <Label className="text-xs text-muted-foreground">Spent</Label>
                <div className="font-mono text-2xl">
                  {formatAmount(spent, budgetUnit)}
                </div>
              </div>
              <div>
                <Label className="text-xs text-muted-foreground">Cap</Label>
                <div className="font-mono text-2xl">
                  {cap > 0 ? formatAmount(cap, budgetUnit) : "∞ unlimited"}
                </div>
              </div>
              <div>
                <Label className="text-xs text-muted-foreground">
                  Currency / Unit
                </Label>
                <div className="font-mono text-sm">{budgetUnit}</div>
              </div>
              <div>
                <Label className="text-xs text-muted-foreground">
                  Period start
                </Label>
                <div className="font-mono text-sm text-muted-foreground">
                  {formatTimestamp(budget.periodStart)}
                </div>
              </div>
              <div>
                <Label className="text-xs text-muted-foreground">
                  Period end
                </Label>
                <div className="font-mono text-sm text-muted-foreground">
                  {budget.periodEnd
                    ? formatTimestamp(budget.periodEnd)
                    : "open-ended"}
                </div>
              </div>
            </div>

            {cap > 0 && (
              <div>
                <div className="flex items-center justify-between text-xs text-muted-foreground">
                  <span>{pct.toFixed(1)}% used</span>
                  {overCap && (
                    <Badge variant="destructive" className={T.labelTight}>
                      <ExclamationTriangleIcon className="mr-1 size-3" />
                      cap reached
                    </Badge>
                  )}
                </div>
                <div className="mt-1 h-2 w-full overflow-hidden rounded bg-muted">
                  <div
                    className={cn(
                      "h-full transition-all",
                      progressColour(spent, cap),
                    )}
                    style={{ width: `${pct}%` }}
                  />
                </div>
              </div>
            )}
          </div>
        ) : null}
      </Card>

      {/* Set form */}
      <Card className="p-6">
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <h3 className="text-base font-medium">Update budget</h3>
            <p className="text-sm text-muted-foreground">
              Set a new cap. Capabilities issued under this tenant see the new
              limit on their next charge attempt.
            </p>
          </div>

          <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
            <div className="space-y-1.5">
              <Label htmlFor="max-budget">Max budget</Label>
              <Input
                id="max-budget"
                type="number"
                step="0.01"
                min={0}
                placeholder="0 = unlimited"
                value={maxBudget}
                onChange={(e) => {
                  setEdited(true);
                  setMaxBudget(e.target.value);
                }}
              />
              <p className={T.hint}>
                0 keeps the counter accumulating without rejecting.
              </p>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="unit-code">Currency / Unit</Label>
              <Select
                options={ALLOWED_UNIT_CODES.map((u) => ({
                  value: u,
                  label: u,
                }))}
                value={unitCode}
                onChange={(v) => {
                  setEdited(true);
                  setUnitCode(v);
                }}
                className="w-full"
              />
              <p className={T.hint}>
                ISO 4217 fiat or UNIT for non-currency metering.
              </p>
            </div>

            <div className="space-y-1.5">
              <Label className="text-xs">Period</Label>
              <label className="flex cursor-pointer items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={resetSpend}
                  onChange={(e) => {
                    setEdited(true);
                    setResetSpend(e.target.checked);
                  }}
                  className="size-4 accent-primary"
                />
                <span>
                  Reset spend (roll the period — typical at billing close).
                </span>
              </label>
              <p className={T.hint}>
                Off: change the cap mid-cycle without affecting accumulated
                spend.
              </p>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="period-end">Period close</Label>
              <Input
                id="period-end"
                type="date"
                value={periodEnd}
                onChange={(e) => {
                  setEdited(true);
                  setPeriodEnd(e.target.value);
                }}
                className="w-full"
              />
              <p className={T.hint}>
                Optional. Pins when the billing window closes; blank leaves the
                server default.
              </p>
            </div>
          </div>

          <div className="flex justify-end">
            <Button
              type="submit"
              disabled={submitting || initialising || !maxBudget.trim()}
            >
              {submitting ? (
                "Updating…"
              ) : (
                <>
                  <CheckCircleIcon className="size-4" />
                  {notFound ? "Create budget" : "Apply changes"}
                </>
              )}
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}
