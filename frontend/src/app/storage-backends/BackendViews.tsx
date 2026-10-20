"use client";

// The storage-backends list's other views (lib/table/viewMode): a tile per
// backend, and a summary of how the backends split. Both show the rows the
// table would, in its order, under its search and state filter.
import Link from "next/link";
import {
  BuildingLibraryIcon,
  GlobeAltIcon,
  HeartIcon,
  PowerIcon,
  ServerStackIcon,
  Squares2X2Icon,
} from "@heroicons/react/24/outline";

import { Breakdown, stateEntries } from "@/components/ui/Breakdown";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { tallyStatus } from "@/components/ui/StatusBar";
import { StatusDot, StatusLabel } from "@/components/ui/StatusIcon";
import { Button } from "@/components/ui/button";
import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import { backendHref } from "@/lib/navigation/hrefs";
import { STORAGE_KIND_LABELS } from "@/lib/storageKind";
import { providerLabel } from "@/lib/storageProvider";
import { tallyBy } from "@/lib/table/viewMode";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { backendStatus, healthView, lifecycleState } from "./backendView";

/** The backend pages a tile links straight to. */
const QUICK_LINKS = [
  { tab: "", label: "Details" },
  { tab: "/buckets", label: "Buckets" },
  { tab: "/features", label: "Features" },
] as const;

/** The grid tiles and summary cards are laid out in. */
const GRID = "grid gap-4 sm:grid-cols-2 xl:grid-cols-3";

/** What the provider reads as when neither the backend nor its endpoint says. */
const NO_PROVIDER = "—";

export function BackendTiles({
  backends,
}: {
  backends: readonly StorageBackend[];
}) {
  return (
    <ul className={GRID} aria-label="Storage backends">
      {backends.map((b) => (
        <li key={b.backendId}>
          <BackendTile backend={b} />
        </li>
      ))}
    </ul>
  );
}

function BackendTile({ backend: b }: { backend: StorageBackend }) {
  const href = backendHref(b.backendId);
  const status = backendStatus(b);
  const state = lifecycleState(b);
  const health = healthView(b);
  const provider = providerLabel(b).label;
  const kind = STORAGE_KIND_LABELS[b.kind] || NO_PROVIDER;
  return (
    <Card size="sm" className="h-full">
      <CardHeader>
        <CardTitle icon={ServerStackIcon} className="min-w-0">
          <Link
            href={href}
            className="min-w-0 truncate hover:text-primary hover:underline"
          >
            {b.backendId}
          </Link>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className={cn(T.hint, "truncate")}>
            {b.displayName || "unnamed"}
          </span>
          <StatusLabel tone={status.tone}>{status.label}</StatusLabel>
        </div>
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <div className="min-w-0">
            <dt className={T.hint}>Storage</dt>
            <dd className="truncate">
              {provider === NO_PROVIDER ? kind : `${provider} · ${kind}`}
            </dd>
          </div>
          <div className="min-w-0">
            <dt className={T.hint}>Region</dt>
            <dd className={cn(T.codeSmall, "truncate")}>{b.region || "—"}</dd>
          </div>
          <div>
            <dt className={T.hint}>State</dt>
            <dd className="flex items-center gap-1.5">
              <StatusDot tone={state.tone} />
              {state.label}
            </dd>
          </div>
          <div>
            <dt className={T.hint}>Health</dt>
            <dd className="flex items-center gap-1.5" title={health.title}>
              <StatusDot tone={health.tone} />
              {health.label}
            </dd>
          </div>
        </dl>
        <div className="flex flex-wrap gap-1">
          {QUICK_LINKS.map((l) => (
            <Button key={l.label} variant="ghost" size="xs" asChild>
              <Link href={`${href}${l.tab}`}>{l.label}</Link>
            </Button>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

export function BackendSummary({
  backends,
}: {
  backends: readonly StorageBackend[];
}) {
  return (
    <div className={GRID}>
      <Breakdown
        title="State"
        icon={PowerIcon}
        entries={stateEntries(tallyStatus(backends, lifecycleState))}
      />
      <Breakdown
        title="Health"
        icon={HeartIcon}
        entries={stateEntries(tallyStatus(backends, healthView))}
      />
      <Breakdown
        title="Provider"
        icon={BuildingLibraryIcon}
        entries={tallyBy(
          backends,
          (b) => {
            const label = providerLabel(b).label;
            return label === NO_PROVIDER ? undefined : label;
          },
          "Not known",
        )}
      />
      <Breakdown
        title="Storage kind"
        icon={Squares2X2Icon}
        entries={tallyBy(backends, (b) => STORAGE_KIND_LABELS[b.kind], "Other")}
      />
      <Breakdown
        title="Region"
        icon={GlobeAltIcon}
        entries={tallyBy(backends, (b) => b.region, "No region")}
      />
    </div>
  );
}
