"use client";

// PolicyHistory — every text one policy layer has held, newest first, with
// who wrote each and when, and any two of them (or one and the editor's draft)
// compared in DiffView. It reads on its own beside the editor: a failed or
// refused read says so here and never stands in the way of editing.

import { useId, useState } from "react";
import { ClockIcon } from "@heroicons/react/24/outline";

import { ActorName } from "@/components/features/audit/ActorName";
import { Timestamp } from "@/components/Timestamp";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DiffView } from "@/components/ui/DiffView";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import type { PolicyVersion } from "@/gen/paladin/admin/v1/policy_service_pb";
import { usePolicyVersions } from "@/hooks/usePolicyVersions";
import {
  CLEARED_LABEL,
  DRAFT_LABEL,
  DRAFT_SIDE,
  WRITER_NOT_RECORDED,
  isCleared,
  layerTenantId,
  writerTenantId,
  resolveComparison,
  sideLabel,
  sideText,
  versionLabel,
  versionName,
  type Comparison,
  type ComparisonSide,
} from "@/lib/policyHistory";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

/** Rows the skeleton stands in for while the first page loads. */
const SKELETON_ROWS = 3;

const CELL = "px-2 py-1.5 align-middle";
const RADIO_CELL = cn(CELL, "w-12 text-center");

function SideRadios({
  group,
  side,
  label,
  comparison,
  pick,
}: {
  /** Names this history's two radio groups apart from another's. */
  group: string;
  side: ComparisonSide;
  /** What a screen reader hears the row as: "v3 · … · …", "Draft". */
  label: string;
  comparison: Comparison | null;
  pick: (change: Partial<Comparison>) => void;
}) {
  return (
    <>
      <td className={RADIO_CELL}>
        <input
          type="radio"
          name={`${group}-before`}
          aria-label={`Compare from ${label}`}
          className="accent-primary"
          checked={comparison?.before === side}
          onChange={() => pick({ before: side })}
        />
      </td>
      <td className={RADIO_CELL}>
        <input
          type="radio"
          name={`${group}-after`}
          aria-label={`Compare to ${label}`}
          className="accent-primary"
          checked={comparison?.after === side}
          onChange={() => pick({ after: side })}
        />
      </td>
    </>
  );
}

function VersionRow({
  group,
  version,
  tenantId,
  comparison,
  pick,
}: {
  group: string;
  version: PolicyVersion;
  tenantId: string;
  comparison: Comparison | null;
  pick: (change: Partial<Comparison>) => void;
}) {
  return (
    <tr className="border-t border-border">
      <SideRadios
        group={group}
        side={versionName(version.version)}
        label={versionLabel(version)}
        comparison={comparison}
        pick={pick}
      />
      <td className={cn(CELL, "font-mono")}>
        {versionName(version.version)}
        {isCleared(version) && (
          <Badge variant="outline" className="ml-2">
            {CLEARED_LABEL}
          </Badge>
        )}
      </td>
      <td className={cn(CELL, "max-w-60 truncate")}>
        {version.writtenBy ? (
          <ActorName
            subject={version.writtenBy}
            tenantId={writerTenantId(version, tenantId)}
          />
        ) : (
          <span className="text-muted-foreground italic">
            {WRITER_NOT_RECORDED}
          </span>
        )}
      </td>
      <td className={cn(CELL, "whitespace-nowrap text-muted-foreground")}>
        <Timestamp ts={version.writtenAt} />
      </td>
    </tr>
  );
}

/**
 * The history of the layer `resourceName` names. `draft` is the editor's
 * unsaved text, offered as one more side to compare; a page with no editor
 * passes none.
 */
export function PolicyHistory({
  resourceName,
  draft,
}: {
  resourceName: string;
  draft?: string;
}) {
  const history = usePolicyVersions(resourceName);
  const group = useId();
  const { versions } = history;
  const [picked, setPicked] = useState<Partial<Comparison>>({});
  // A pick on another layer does not carry over — render-phase
  // adjust-on-change, not set-state-in-effect.
  const [pickedFor, setPickedFor] = useState(resourceName);
  if (pickedFor !== resourceName) {
    setPickedFor(resourceName);
    setPicked({});
  }
  const pick = (change: Partial<Comparison>) =>
    setPicked((p) => ({ ...p, ...change }));
  const comparison = resolveComparison(picked, versions, draft);
  const tenantId = layerTenantId(resourceName);

  return (
    <section aria-label="Policy history" className="space-y-3">
      <div className="space-y-0.5">
        <p className="flex items-center gap-1.5 text-sm font-medium">
          <ClockIcon aria-hidden className="size-4" />
          Policy history
        </p>
        <p className={T.hint}>
          Every text this layer has held, newest first. Pick two to compare.
        </p>
      </div>

      {history.error ? (
        <ListLoadError
          variant="inline"
          what="The policy history"
          reason={history.error}
          onRetry={() => void history.refresh()}
        />
      ) : history.notFound ? (
        <p role="status" className={T.hint}>
          This layer has no history to show: it does not exist, or is not
          visible to you.
        </p>
      ) : history.loading ? (
        <div className="space-y-2">
          {Array.from({ length: SKELETON_ROWS }, (_, i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </div>
      ) : (
        <>
          {versions.length < 2 && (
            <p role="status" className={T.hint}>
              No earlier versions.
            </p>
          )}
          {versions.length > 0 && (
            <div className="overflow-x-auto rounded-md border border-border">
              <table aria-label="Policy versions" className="w-full text-xs">
                <thead className="bg-muted/30 text-left text-muted-foreground">
                  <tr>
                    <th className={RADIO_CELL}>From</th>
                    <th className={RADIO_CELL}>To</th>
                    <th className={CELL}>Version</th>
                    <th className={CELL}>Written by</th>
                    <th className={CELL}>Written at</th>
                  </tr>
                </thead>
                <tbody>
                  {draft !== undefined && (
                    <tr className="border-t border-border">
                      <SideRadios
                        group={group}
                        side={DRAFT_SIDE}
                        label={DRAFT_LABEL}
                        comparison={comparison}
                        pick={pick}
                      />
                      <td className={CELL} colSpan={3}>
                        {DRAFT_LABEL}
                        <span className="ml-2 text-muted-foreground">
                          the editor&apos;s text, unsaved
                        </span>
                      </td>
                    </tr>
                  )}
                  {versions.map((v) => (
                    <VersionRow
                      key={v.version}
                      group={group}
                      version={v}
                      tenantId={tenantId}
                      comparison={comparison}
                      pick={pick}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {history.hasMore && (
            <div className="flex justify-center">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void history.loadMore()}
                disabled={history.loadingMore}
              >
                {history.loadingMore ? "Loading…" : "Load older"}
              </Button>
            </div>
          )}
          {comparison && (
            <DiffView
              before={sideText(comparison.before, versions, draft) ?? ""}
              after={sideText(comparison.after, versions, draft) ?? ""}
              beforeLabel={sideLabel(comparison.before)}
              afterLabel={sideLabel(comparison.after)}
            />
          )}
        </>
      )}
    </section>
  );
}
