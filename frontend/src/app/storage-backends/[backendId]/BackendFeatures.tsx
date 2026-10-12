"use client";

// BackendFeatures — which S3 features this backend's last probe found
// (ADR-0026), and what the gaps mean. A self-hosted store implements its own
// subset of S3; this card is where an operator learns, before a tenant does,
// that uploads here could replace objects or that public collections cannot
// be created.
//
// Required features first, then optional ones, one line each: whether it
// works, and what Paladin uses it for. What the probe actually saw — often a
// multi-line SDK error — stays folded under the row until asked for.

import {
  CheckCircleIcon,
  ExclamationTriangleIcon,
  InformationCircleIcon,
  QuestionMarkCircleIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

import {
  FeatureSupport,
  type StorageBackend,
  type StorageFeatureSupport,
} from "@/gen/paladin/admin/v1/types_pb";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { formatDateTime } from "@/lib/format/locale";
import {
  FEATURE_LABELS,
  SUPPORT_LABELS,
  compatibilityBadge,
  featureWarnings,
  type WarningLevel,
} from "@/lib/storageFeatures";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { cn } from "@/lib/utils";

type Backend = Pick<StorageBackend, "features" | "compatibility">;

// Severity order of the warnings box: its tint follows the worst line in it.
const LEVEL_RANK: Record<WarningLevel, number> = {
  info: 0,
  warning: 1,
  error: 2,
};

const WARNING_STYLE: Record<
  WarningLevel,
  { icon: typeof XCircleIcon; text: string; box: string }
> = {
  error: {
    icon: XCircleIcon,
    text: "text-destructive",
    box: "border-destructive/30 bg-destructive/5",
  },
  warning: {
    icon: ExclamationTriangleIcon,
    text: "text-chart-3",
    box: "border-chart-3/30 bg-chart-3/5",
  },
  info: {
    icon: InformationCircleIcon,
    text: "text-muted-foreground",
    box: "border-border bg-muted/30",
  },
};

const SUPPORT_STYLE: Record<
  FeatureSupport,
  {
    icon: typeof XCircleIcon;
    text: string;
    variant: "success" | "destructive" | "secondary";
  }
> = {
  [FeatureSupport.UNSPECIFIED]: {
    icon: QuestionMarkCircleIcon,
    text: "text-muted-foreground",
    variant: "secondary",
  },
  [FeatureSupport.SUPPORTED]: {
    icon: CheckCircleIcon,
    text: "text-chart-2",
    variant: "success",
  },
  [FeatureSupport.UNSUPPORTED]: {
    icon: XCircleIcon,
    text: "text-destructive",
    variant: "destructive",
  },
  [FeatureSupport.UNKNOWN]: {
    icon: QuestionMarkCircleIcon,
    text: "text-muted-foreground",
    variant: "secondary",
  },
};

/** When the most recent probe of any feature ran; undefined if none has. */
export function lastProbed(backend: Backend): Date | undefined {
  let latest: Date | undefined;
  for (const f of backend.features) {
    if (!f.checkedAt) continue;
    const at = timestampDate(f.checkedAt);
    if (!latest || at > latest) latest = at;
  }
  return latest;
}

export function BackendFeatures({ backend }: { backend: Backend }) {
  const warnings = featureWarnings(backend);
  const compat = compatibilityBadge(backend);
  const probedAt = lastProbed(backend);
  const supported = backend.features.filter(
    (f) => f.support === FeatureSupport.SUPPORTED,
  ).length;
  const required = backend.features.filter((f) => f.required);
  const optional = backend.features.filter((f) => !f.required);
  const worst = warnings.reduce<WarningLevel>(
    (acc, w) => (LEVEL_RANK[w.level] > LEVEL_RANK[acc] ? w.level : acc),
    "info",
  );

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
          <CardTitle className="text-sm font-semibold tracking-tight">
            S3 features
          </CardTitle>
          <div className="flex items-center gap-3 text-xs text-muted-foreground">
            <span>
              {supported} of {backend.features.length} supported
            </span>
            <span aria-hidden>·</span>
            <span>
              {probedAt ? `Probed ${formatDateTime(probedAt)}` : "Never probed"}
            </span>
            <Badge variant={compat.variant}>{compat.label}</Badge>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-4 pt-0">
        {warnings.length > 0 ? (
          <ul
            className={cn(
              "space-y-1.5 rounded-md border px-3 py-2",
              WARNING_STYLE[worst].box,
            )}
            aria-label="Backend warnings"
          >
            {warnings.map((w) => {
              const { icon: Icon, text } = WARNING_STYLE[w.level];
              return (
                <li
                  key={w.text}
                  className={cn("flex items-start gap-2 text-sm", text)}
                >
                  <Icon className="mt-0.5 size-4 shrink-0" />
                  <span>{w.text}</span>
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="flex items-center gap-2 text-sm text-chart-2">
            <CheckCircleIcon className="size-4" />
            Every feature Paladin uses is supported.
          </p>
        )}

        <FeatureGroup title="Required by Paladin" features={required} />
        <FeatureGroup title="Optional" features={optional} />
      </CardContent>
    </Card>
  );
}

function FeatureGroup({
  title,
  features,
}: {
  title: string;
  features: StorageFeatureSupport[];
}) {
  if (features.length === 0) return null;
  return (
    <section aria-label={title}>
      <h3 className="mb-1 text-xs font-medium text-muted-foreground">
        {title}
      </h3>
      <ul className="divide-y divide-border rounded-md border border-border">
        {features.map((f) => (
          <FeatureRow key={f.feature} feature={f} />
        ))}
      </ul>
    </section>
  );
}

function FeatureRow({ feature: f }: { feature: StorageFeatureSupport }) {
  const style = SUPPORT_STYLE[f.support];
  const Icon = style.icon;
  // A supported feature's message says nothing an operator needs; anything
  // else carries what the store answered, which is the lead to follow.
  const output = f.support !== FeatureSupport.SUPPORTED ? f.message : "";
  return (
    <li className="px-3 py-2.5">
      <div className="flex items-start gap-3">
        <Icon
          className={cn("mt-0.5 size-5 shrink-0", style.text)}
          aria-hidden
        />
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium">{FEATURE_LABELS[f.feature]}</div>
          <div className="text-xs text-muted-foreground">
            Needed for {f.enables}
          </div>
        </div>
        <Badge
          variant={style.variant}
          title={
            f.checkedAt
              ? `Probed ${formatDateTime(timestampDate(f.checkedAt))}`
              : "Never probed"
          }
        >
          {SUPPORT_LABELS[f.support]}
        </Badge>
      </div>
      {output && (
        <details className="group mt-2 ml-8">
          <summary className="cursor-pointer text-xs text-muted-foreground select-none hover:text-foreground">
            Probe output
          </summary>
          <pre className="mt-1.5 max-h-48 overflow-auto rounded-md bg-muted/50 p-2 font-mono text-xs whitespace-pre-wrap break-words text-muted-foreground">
            {output}
          </pre>
        </details>
      )}
    </li>
  );
}
