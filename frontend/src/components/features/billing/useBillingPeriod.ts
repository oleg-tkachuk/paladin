"use client";

import { useCallback, useMemo, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import type { BillingPeriod } from "@/lib/queries/billing";
import { isPeriod, resolvePeriod, type Period, type PresetKey } from "./period";
import {
  periodFromParams,
  querySuffix,
  withPeriod,
  type PeriodChoice,
} from "./periodParams";

/** The billing period a page shows, kept in its URL. */
export interface BillingPeriodState {
  choice: PeriodChoice;
  /** The period chosen, or why it is not one (a custom span not yet set). */
  resolved: Period | { error: string };
  period: Period | null;
  /** The period as the API takes it; null until it is one. */
  range: BillingPeriod | null;
  setPreset: (preset: PresetKey) => void;
  setFrom: (from: string) => void;
  setTo: (to: string) => void;
  /** Reads a preset period again up to now. */
  refresh: () => void;
  /** The query that carries the period, for a link to another page. */
  query: string;
}

export function useBillingPeriod(): BillingPeriodState {
  const router = useRouter();
  const pathname = usePathname();
  const search = useSearchParams();
  const choice = useMemo(
    () => periodFromParams(new URLSearchParams(search.toString())),
    [search],
  );
  // "Now" for a preset, held until a refresh: a fresh instant per render
  // would make every render a new query.
  const [asOf, setAsOf] = useState(() => new Date());
  const resolved = useMemo(
    () => resolvePeriod(choice.preset, choice.from, choice.to, asOf),
    [choice, asOf],
  );
  const period = isPeriod(resolved) ? resolved : null;
  const range = useMemo(
    () =>
      period && {
        periodStart: timestampFromDate(period.start),
        periodEnd: timestampFromDate(period.end),
      },
    [period],
  );
  const write = useCallback(
    (next: PeriodChoice) => {
      const params = withPeriod(new URLSearchParams(search.toString()), next);
      router.replace(`${pathname}${querySuffix(params)}`);
    },
    [router, pathname, search],
  );
  return {
    choice,
    resolved,
    period,
    range,
    setPreset: (preset) => write({ ...choice, preset }),
    setFrom: (from) => write({ ...choice, from }),
    setTo: (to) => write({ ...choice, to }),
    refresh: useCallback(() => setAsOf(new Date()), []),
    query: querySuffix(withPeriod(new URLSearchParams(), choice)),
  };
}
