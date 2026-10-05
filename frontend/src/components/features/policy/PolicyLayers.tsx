"use client";

// PolicyLayers — the layers GetEffectivePolicy returns, in the order the
// authorizer compiles them: the built-in layer, then tenant → bucket →
// collection. A layer whose stored text does not compile is marked frozen and
// shows what is evaluated in its place, since that — not the stored text — is
// what decides a request.

import type { PolicyLayer } from "@/gen/paladin/admin/v1/policy_service_pb";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

const codeClass = cn(
  T.codeSmall,
  "overflow-x-auto whitespace-pre-wrap leading-relaxed text-muted-foreground",
);

export function PolicyLayers({ layers }: { layers: PolicyLayer[] }) {
  return (
    <div className="space-y-2">
      {layers.map((layer, i) => (
        <div
          key={`${layer.source}-${i}`}
          className="rounded-md border bg-muted/30 p-2"
        >
          <div className="mb-1 flex flex-wrap items-center gap-2 text-xs">
            <Badge variant="outline" className="font-mono break-all">
              {layer.source || "(unknown)"}
            </Badge>
            {layer.frozen && (
              <Badge variant="destructive">Frozen — does not compile</Badge>
            )}
          </div>
          {layer.frozen ? (
            <div className="space-y-2">
              <p className="text-xs text-muted-foreground">
                The stored text below does not compile, so everything in this
                layer&apos;s scope is denied except replacing the layer.
                Evaluated in its place:
              </p>
              <pre className={codeClass}>{layer.evaluatedCedarPolicy}</pre>
              <p className="text-xs text-muted-foreground">Stored:</p>
              <pre className={cn(codeClass, "line-through opacity-70")}>
                {layer.cedarPolicy}
              </pre>
            </div>
          ) : (
            <pre className={codeClass}>{layer.cedarPolicy || "(empty)"}</pre>
          )}
        </div>
      ))}
    </div>
  );
}
