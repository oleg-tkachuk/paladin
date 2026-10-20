"use client";

import { Input } from "@/components/ui/input";
import { formatDate } from "@/lib/format/locale";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { PRESETS } from "./period";
import { Segmented } from "./Segmented";
import type { BillingPeriodState } from "./useBillingPeriod";

/** The period a billing page shows: a preset, or a custom span of dates. */
export function BillingPeriodBar({ state }: { state: BillingPeriodState }) {
  const { choice, period, resolved } = state;
  return (
    <div className="flex flex-wrap items-center gap-3">
      <Segmented
        label="Period"
        options={PRESETS.map((p) => ({ value: p.key, label: p.label }))}
        value={choice.preset}
        onChange={state.setPreset}
      />
      {choice.preset === "custom" ? (
        <div className="flex items-center gap-2">
          <Input
            type="date"
            value={choice.from}
            onChange={(e) => state.setFrom(e.target.value)}
            className="w-40"
            aria-label="Period start"
          />
          <span className={T.helper}>to</span>
          <Input
            type="date"
            value={choice.to}
            onChange={(e) => state.setTo(e.target.value)}
            className="w-40"
            aria-label="Period end"
          />
        </div>
      ) : null}
      <p className={cn(T.helper, !period && "text-destructive")}>
        {period
          ? `${formatDate(period.start)} – ${formatDate(period.end)}`
          : "error" in resolved
            ? resolved.error
            : null}
      </p>
    </div>
  );
}
