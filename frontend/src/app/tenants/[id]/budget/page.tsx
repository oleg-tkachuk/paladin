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
import { notFoundIsAnswer } from "@/lib/connect/expected";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import {
  ABSTRACT_UNIT_CODE,
  ALLOWED_UNIT_CODES,
  formatMoney,
  moneyFromDecimal,
  moneyIsPositive,
  moneyPercent,
  moneyToDecimal,
  moneyToNanos,
  NANOS_DECIMALS,
  unitOf,
} from "@/lib/format/money";
import type { Money } from "@/gen/google/type/money_pb";
import { Select } from "@/components/ui/Select";
import { isAbortError, errorMessage } from "@/hooks/errorContract";

import { useTenant, useTenantChangesBlocked } from "../tenant-context";
import { Timestamp } from "@/components/Timestamp";

function progressColour(
  spent: Money | undefined,
  max: Money | undefined,
): string {
  const pct = moneyPercent(spent, max);
  if (pct === null) return "bg-primary/40";
  if (pct >= 90) return "bg-destructive";
  if (pct >= 70) return "bg-warning";
  return "bg-success";
}

export default function TenantBudgetPage() {
  const changesBlocked = useTenantChangesBlocked();
  const tenant = useTenant();
  const tenantId = tenant.tenantId;
  const { showNotification } = useNotification();

  const budgetQuery = useQuery({
    queryKey: ["tenantBudget", tenantId],
    retry: false, // queryFn toasts real failures; NotFound is a normal state.
    queryFn: async ({ signal }) => {
      try {
        const res = await tenantBudgetClient.get(
          { tenantId },
          notFoundIsAnswer({ signal }),
        );
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
          message: errorMessage(err, "Failed to load tenant budget"),
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
  // XXX — ISO 4217's "no currency" — so the page doesn't assume
  // operators want USD. The hydration below replaces this with the
  // budget's unit whenever a snapshot loads.
  const [unitCode, setUnitCode] = useState<string>(ABSTRACT_UNIT_CODE);
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
      setMaxBudget(moneyToDecimal(budget.maxBudget));
      const stored =
        budget.maxBudget?.currencyCode || budget.spent?.currencyCode;
      if (stored) setUnitCode(stored);
      setPeriodEnd(
        budget.periodEnd
          ? new Date(Number(budget.periodEnd.seconds) * 1000)
              .toISOString()
              .slice(0, 10)
          : "",
      );
    } else {
      setMaxBudget("");
      setUnitCode(ABSTRACT_UNIT_CODE);
      setPeriodEnd("");
    }
    setResetSpend(false);
  }

  const handleSubmit = async (e?: React.FormEvent) => {
    e?.preventDefault();
    // Straight to Money, never through a float: "0.1" is exactly 100000000
    // nanos. Zero is sent too: it is unlimited in the chosen unit.
    const cap = moneyFromDecimal(maxBudget || "0", unitCode);
    if (cap === null) {
      showNotification({
        type: "error",
        title: "Validation",
        message: `Max budget must be a non-negative amount with at most ${NANOS_DECIMALS} decimals.`,
      });
      return;
    }
    setSubmitting(true);
    try {
      await tenantBudgetClient.set({
        tenantId,
        maxBudget: cap,
        resetSpend,
        // OCC guard. "0" asserts no row exists yet — the create case — and is
        // itself rejected if someone created one in the meantime. Anything
        // else is the version the form was filled from: not the latest read,
        // which a refetch during an edit moves forward while the form keeps
        // the operator's values, and which would then carry a stale edit
        // straight over another operator's change.
        resourceVersion: seededFrom?.resourceVersion || "0",
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
          ? `Cap set to ${formatMoney(cap)} and period rolled.`
          : `Cap set to ${formatMoney(cap)}.`,
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
      const msg = errorMessage(err, "Update failed");
      showNotification({ type: "error", title: "Update failed", message: msg });
    } finally {
      setSubmitting(false);
    }
  };

  const spent = budget?.spent;
  const cap = budget?.maxBudget;
  // Display fallback when an existing budget row carries no unit.
  // XXX is correct: rendering "$0.00" for what's actually metering
  // would lie about the configured currency.
  const budgetUnit = unitOf(cap?.currencyCode ? cap : spent);
  const capped = moneyIsPositive(cap);
  const pct = Math.min(100, moneyPercent(spent, cap) ?? 0);
  const overCap = capped && moneyToNanos(spent) >= moneyToNanos(cap);

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
                  {formatMoney(spent, { fallbackUnit: budgetUnit })}
                </div>
              </div>
              <div>
                <Label className="text-xs text-muted-foreground">Cap</Label>
                <div className="font-mono text-2xl">
                  {capped
                    ? formatMoney(cap, { fallbackUnit: budgetUnit })
                    : "∞ unlimited"}
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
                  <Timestamp ts={budget.periodStart} />
                </div>
              </div>
              <div>
                <Label className="text-xs text-muted-foreground">
                  Period end
                </Label>
                <div className="font-mono text-sm text-muted-foreground">
                  {budget.periodEnd ? (
                    <Timestamp ts={budget.periodEnd} />
                  ) : (
                    "open-ended"
                  )}
                </div>
              </div>
            </div>

            {capped && (
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
        ) : budgetQuery.isError ? (
          <div className="flex items-start gap-3 text-sm">
            <BanknotesIcon className="mt-0.5 size-5 text-muted-foreground" />
            <div>
              <p className="font-medium">Budget unavailable.</p>
              <p className="text-muted-foreground">
                {errorMessage(budgetQuery.error)}
              </p>
            </div>
          </div>
        ) : null}
      </Card>

      {/* Set form */}
      <Card className="p-6">
        <form onSubmit={handleSubmit}>
          {/* Locked until the first snapshot lands. A value typed before it
              marked the form edited, which kept the snapshot — and its
              version — from seeding, so Apply sent version "0" and the
              server refused it as a conflict. */}
          <fieldset disabled={initialising} className="space-y-4">
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
                  // Amounts are exact to the nano; a coarser step would have
                  // the browser refuse a finer one before handleSubmit sees it.
                  step="any"
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
                  id="unit-code"
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
                  ISO 4217 fiat, or XXX for metering that is not money.
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
                  Optional. Pins when the billing window closes; blank leaves
                  the server default.
                </p>
              </div>
            </div>

            <div className="flex justify-end">
              <Button
                type="submit"
                title={changesBlocked ?? undefined}
                disabled={
                  Boolean(changesBlocked) ||
                  submitting ||
                  initialising ||
                  budgetQuery.isError ||
                  !maxBudget.trim()
                }
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
          </fieldset>
        </form>
      </Card>
    </div>
  );
}
