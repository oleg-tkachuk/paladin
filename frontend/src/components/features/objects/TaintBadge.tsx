"use client";

import { ShieldExclamationIcon } from "@heroicons/react/24/outline";

import type { TaintSignal } from "@/gen/paladin/data/v1/types_pb";
import { Badge } from "@/components/ui/badge";
import { TAINT_SIGNALS, taintLabel } from "@/lib/objects/taint";

/**
 * An object's taint, where it is named: on its page and in its panel. Nothing
 * for an object with no signal. The badge says why an agent is refused the
 * object; its title spells each signal out.
 */
export function TaintBadge({ signals }: { signals: readonly TaintSignal[] }) {
  if (signals.length === 0) return null;
  const title = TAINT_SIGNALS.filter((s) => signals.includes(s.signal))
    .map((s) => `${s.label}: ${s.description}`)
    .join("\n");
  return (
    <Badge variant="warning" title={title} data-testid="taint-badge">
      <ShieldExclamationIcon className="size-3.5" aria-hidden="true" />
      Tainted · {taintLabel(signals)}
    </Badge>
  );
}
