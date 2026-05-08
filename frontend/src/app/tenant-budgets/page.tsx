"use client";

import { useCallback, useEffect, useState } from "react";
import {
  ArrowPathIcon,
  BanknotesIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";
import { ConnectError, Code } from "@connectrpc/connect";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { useNotification } from "@/components/ui/Notification";
import { useTenant } from "@/context/TenantContext";
import { tenantBudgetClient } from "@/lib/connect/client";
import type { TenantBudget } from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import { cn } from "@/lib/utils";

// /tenant-budgets — admin surface for the per-tenant aggregate USD
// spend cap that backs the capability subsystem's two-phase Charge.
//
// The active tenant's row is the only one rendered (operators
// switch tenant via the existing TenantSwitcher in the top bar).
// Two views compose the page:
//
//   1. Snapshot — reads tenant_budgets via TenantBudgetService.Get.
//      Renders max_budget_usd / spent_usd / period dates, plus a
//      progress bar that turns amber > 70 % and red > 90 %.
//
//   2. Set form — issues TenantBudgetService.Set. ResetSpend rolls
//      the period (zeros spent_usd, period_start = now); off
//      changes the cap mid-cycle. PeriodEnd is optional — pin a
//      closing time to make the dashboard render countdown.
//
// NOT_FOUND is the cold-start path: tenant has no budget row yet.
// The UI flips to "no budget configured" + a Set form to create
// the first one. Setting a non-zero cap also activates server-side
// enforcement (the runtime path was wired in the previous backend
// commit; the operator's job here is to pick numbers).

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

function formatUSD(n: number): string {
  if (!Number.isFinite(n)) return "—";
  return n.toLocaleString("en-US", { style: "currency", currency: "USD" });
}

// progressColour clamps to three buckets: < 70 % green, 70-90 amber,
// > 90 red. Operators usually act around the 80 % mark; the colour
// shift cues the eye without requiring a numeric scan.
function progressColour(spent: number, max: number): string {
  if (max <= 0) return "bg-primary/40";
  const pct = (spent / max) * 100;
  if (pct >= 90) return "bg-destructive";
  if (pct >= 70) return "bg-amber-500";
  return "bg-emerald-500";
}

export default function TenantBudgetsPage() {
  const { tenantId, tenant } = useTenant();
  const { showNotification } = useNotification();

  // ── snapshot state ──────────────────────────────────────────────────
  const [budget, setBudget] = useState<TenantBudget | null>(null);
  const [loading, setLoading] = useState(false);
  const [notFound, setNotFound] = useState(false);

  const fetchBudget = useCallback(async () => {
    if (!tenantId) return;
    setLoading(true);
    setNotFound(false);
    try {
      const res = await tenantBudgetClient.get({ tenantId });
      setBudget(res.budget ?? null);
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.NotFound) {
        setBudget(null);
        setNotFound(true);
        return;
      }
      const msg =
        err instanceof ConnectError
          ? err.rawMessage
          : "Failed to load tenant budget";
      showNotification({
        type: "error",
        title: "Load failed",
        message: msg,
      });
    } finally {
      setLoading(false);
    }
  }, [tenantId, showNotification]);

  useEffect(() => {
    void fetchBudget();
  }, [fetchBudget]);

  // ── set form state ──────────────────────────────────────────────────
  const [maxBudget, setMaxBudget] = useState<string>("");
  const [resetSpend, setResetSpend] = useState<boolean>(false);
  const [submitting, setSubmitting] = useState(false);

  // Whenever the snapshot loads, populate the form with the current
  // cap so editing is "tweak this" rather than "type from scratch".
  useEffect(() => {
    if (budget) {
      setMaxBudget(String(budget.maxBudgetUsd));
    } else {
      setMaxBudget("");
    }
    setResetSpend(false);
  }, [budget]);

  const handleSubmit = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!tenantId) return;
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
      const res = await tenantBudgetClient.set({
        tenantId,
        maxBudgetUsd: cap,
        resetSpend,
      });
      setBudget(res.budget ?? null);
      setNotFound(false);
      showNotification({
        type: "success",
        title: "Budget updated",
        message: resetSpend
          ? `Cap set to ${formatUSD(cap)} and period rolled.`
          : `Cap set to ${formatUSD(cap)}.`,
      });
    } catch (err) {
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Update failed";
      showNotification({
        type: "error",
        title: "Update failed",
        message: msg,
      });
    } finally {
      setSubmitting(false);
    }
  };

  const spent = Number(budget?.spentUsd ?? 0);
  const cap = Number(budget?.maxBudgetUsd ?? 0);
  const pct = cap > 0 ? Math.min(100, (spent / cap) * 100) : 0;
  const overCap = cap > 0 && spent >= cap;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Tenant Budgets"
        description={
          tenant
            ? `Aggregate USD spend cap for ${tenant.displayName ?? tenant.name ?? "tenant"}.`
            : "Aggregate USD spend cap for the active tenant."
        }
        showDefaultActions={false}
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => void fetchBudget()}
            disabled={loading || !tenantId}
          >
            <ArrowPathIcon
              className={cn("size-4", loading && "animate-spin")}
            />
            Refresh
          </Button>
        }
      />

      {/* ─── Snapshot card ──────────────────────────────────────────── */}
      <Card className="p-6">
        {!tenantId ? (
          <div className="text-sm text-muted-foreground">
            Pick a tenant from the top bar to manage its budget.
          </div>
        ) : loading && !budget ? (
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
                <div className="font-mono text-2xl">{formatUSD(spent)}</div>
              </div>
              <div>
                <Label className="text-xs text-muted-foreground">Cap</Label>
                <div className="font-mono text-2xl">
                  {cap > 0 ? formatUSD(cap) : "∞ unlimited"}
                </div>
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
                    <Badge variant="destructive" className="text-[10px]">
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

      {/* ─── Set form ───────────────────────────────────────────────── */}
      {tenantId && (
        <Card className="p-6">
          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <h2 className="text-base font-medium">Update budget</h2>
              <p className="text-sm text-muted-foreground">
                Set a new cap. Capabilities issued under this tenant will see
                the new limit on their next charge attempt.
              </p>
            </div>

            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="max-budget">Max budget (USD)</Label>
                <Input
                  id="max-budget"
                  type="number"
                  step="0.01"
                  min={0}
                  placeholder="0 = unlimited"
                  value={maxBudget}
                  onChange={(e) => setMaxBudget(e.target.value)}
                />
                <p className="text-[11px] text-muted-foreground">
                  0 keeps the counter accumulating without rejecting.
                </p>
              </div>

              <div className="space-y-1.5">
                <Label className="text-xs">Period</Label>
                <label className="flex cursor-pointer items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={resetSpend}
                    onChange={(e) => setResetSpend(e.target.checked)}
                    className="size-4 accent-primary"
                  />
                  <span>
                    Reset spend (roll the period — typical at billing close).
                  </span>
                </label>
                <p className="text-[11px] text-muted-foreground">
                  Off: change the cap mid-cycle without affecting accumulated
                  spend.
                </p>
              </div>
            </div>

            <div className="flex justify-end">
              <Button type="submit" disabled={submitting || !maxBudget.trim()}>
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
      )}
    </div>
  );
}
