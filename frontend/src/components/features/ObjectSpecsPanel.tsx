import React from "react";
import Link from "next/link";

import { ClipboardIcon } from "@heroicons/react/24/outline";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import { cn, formatBytes } from "@/lib/utils";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { IdentifierCopy } from "@/components/ui/IdentifierCopy";
import { T } from "@/lib/ui/typography";

// SpecRow renders one fact in the Specs sidebar — label above value, value
// gets full sidebar width to wrap into. The row's flex layout keeps the value
// and the trailing action on the same baseline while letting a long mono value
// (UUID, path) `break-all` wrap.
function SpecRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-0.5">
      <dt className={T.label}>{label}</dt>
      <dd className="flex min-w-0 items-start gap-2">{children}</dd>
    </div>
  );
}

function formatExpiresAt(seconds: bigint | undefined): string {
  if (!seconds) return "Never";
  try {
    return new Date(Number(seconds) * 1000).toLocaleString();
  } catch {
    return "—";
  }
}

function stateColorClasses(state: ObjectState): { text: string; dot: string } {
  if (state === ObjectState.AVAILABLE)
    return { text: "text-chart-2", dot: "bg-chart-2" };
  if (state === ObjectState.DELETED)
    return { text: "text-destructive", dot: "bg-destructive" };
  return { text: "text-chart-3", dot: "bg-chart-3" };
}

/**
 * Read-only "Specs" sidebar for the object detail view, extracted from
 * ObjectDetailView. One facts card: identity group (UUID, storage path,
 * object_key link), a separator, then physical + lifecycle attributes. The
 * reserved `object_tag` is surfaced here as Classification (the Tags card
 * filters it out so the value never renders twice). Copying the storage path
 * delegates to the page's clipboard helper via onCopy.
 */
export function ObjectSpecsPanel({
  object,
  collection,
  onCopy,
}: {
  object: Object$;
  collection: string;
  onCopy: (text: string, label: string) => void;
}) {
  const stateColors = stateColorClasses(object.state);
  return (
    <aside className="space-y-4">
      <Card className="space-y-3 p-4">
        <h2 className="text-sm font-semibold">Specs</h2>
        <dl className="space-y-3">
          <SpecRow label="Object UUID">
            <span className="break-all font-mono text-xs">
              {object.objectId || collection}
            </span>
            <IdentifierCopy
              value={object.objectId || collection}
              label="Object UUID"
              iconOnly
            />
          </SpecRow>

          <SpecRow label="Storage Path">
            <span className="break-all font-mono text-xs" title={object.key}>
              {object.key}
            </span>
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={() => onCopy(object.key, "Storage path")}
              aria-label="Copy storage path"
            >
              <ClipboardIcon className="size-3.5" />
            </Button>
          </SpecRow>

          <SpecRow label="Collection">
            {/* A Collection lives under its tenant; /collections/<name> is no
                page, and the link led to a 404. */}
            <Link
              href={`/tenants/${encodeURIComponent(object.tenantId)}/collections/${encodeURIComponent(object.collection)}`}
              className="break-all font-mono text-xs text-primary hover:underline"
            >
              {object.collection}
            </Link>
          </SpecRow>

          <Separator />

          <SpecRow label="Size">
            <span className="font-mono text-xs">
              {formatBytes(object.sizeBytes)}
            </span>
          </SpecRow>

          <SpecRow label="Type">
            <span className="break-all font-mono text-xs">
              {object.contentType || "—"}
            </span>
          </SpecRow>

          <SpecRow label="State">
            <span className={cn(T.pill, stateColors.text)}>
              <span className={cn(T.pillDot, stateColors.dot)} />
              {ObjectState[object.state]}
            </span>
          </SpecRow>

          {/* Classification = the reserved `object_tag` key inside the same
              `tags` map the Tags card edits. One-of, not bag-of, so it lives
              here rather than in the Tags card (which filters it out so the
              same value never renders twice). */}
          <SpecRow label="Classification">
            {object.tags?.object_tag ? (
              <span className="font-mono text-xs">
                {object.tags.object_tag}
              </span>
            ) : (
              <span className="font-mono text-xs text-muted-foreground">—</span>
            )}
          </SpecRow>

          <SpecRow label="Expires">
            <span className="font-mono text-xs">
              {formatExpiresAt(object.presignExpiresAt?.seconds)}
            </span>
          </SpecRow>

          <SpecRow label="External Ref">
            <span className="break-all font-mono text-xs">
              {object.externalRef || "—"}
            </span>
          </SpecRow>
        </dl>
      </Card>
    </aside>
  );
}
