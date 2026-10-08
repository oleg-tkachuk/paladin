"use client";

// BackendFeatures — which S3 features this backend's last probe found
// (ADR-0026), and what the gaps mean. A self-hosted store implements its own
// subset of S3; this card is where an operator learns, before a tenant does,
// that uploads here could replace objects or that public collections cannot
// be created.

import {
  CheckCircleIcon,
  ExclamationTriangleIcon,
  InformationCircleIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

import {
  FeatureSupport,
  type StorageBackend,
} from "@/gen/paladin/admin/v1/types_pb";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { formatDateTime } from "@/lib/format/locale";
import {
  FEATURE_LABELS,
  SUPPORT_LABELS,
  featureWarnings,
  type WarningLevel,
} from "@/lib/storageFeatures";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

const WARNING_STYLE: Record<
  WarningLevel,
  { icon: typeof XCircleIcon; className: string }
> = {
  error: { icon: XCircleIcon, className: "text-destructive" },
  warning: { icon: ExclamationTriangleIcon, className: "text-chart-3" },
  info: { icon: InformationCircleIcon, className: "text-muted-foreground" },
};

const SUPPORT_VARIANT: Record<
  FeatureSupport,
  "success" | "destructive" | "secondary"
> = {
  [FeatureSupport.UNSPECIFIED]: "secondary",
  [FeatureSupport.SUPPORTED]: "success",
  [FeatureSupport.UNSUPPORTED]: "destructive",
  [FeatureSupport.UNKNOWN]: "secondary",
};

export function BackendFeatures({
  backend,
}: {
  backend: Pick<StorageBackend, "features" | "compatibility">;
}) {
  const warnings = featureWarnings(backend);
  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="text-sm font-semibold tracking-tight">
          S3 features
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 pt-0">
        {warnings.length > 0 ? (
          <ul className="space-y-1.5" aria-label="Backend warnings">
            {warnings.map((w) => {
              const { icon: Icon, className } = WARNING_STYLE[w.level];
              return (
                <li
                  key={w.text}
                  className={cn("flex items-start gap-2 text-xs", className)}
                >
                  <Icon className="mt-px size-4 shrink-0" />
                  <span>{w.text}</span>
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="flex items-center gap-2 text-xs text-chart-2">
            <CheckCircleIcon className="size-4" />
            Every feature Paladin uses is supported.
          </p>
        )}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Feature</TableHead>
              <TableHead>Needed for</TableHead>
              <TableHead>Result</TableHead>
              <TableHead className="hidden @3xl:table-cell">Detail</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {backend.features.map((f) => (
              <TableRow key={f.feature}>
                <TableCell className="font-medium whitespace-normal">
                  {FEATURE_LABELS[f.feature]}
                  {f.required && (
                    <Badge
                      variant="outline"
                      className={cn("ml-2", T.labelTight)}
                    >
                      Required
                    </Badge>
                  )}
                </TableCell>
                {/* Prose columns wrap rather than widening the table. */}
                <TableCell className="text-xs whitespace-normal text-muted-foreground">
                  {f.enables}
                </TableCell>
                <TableCell>
                  <Badge
                    variant={SUPPORT_VARIANT[f.support]}
                    className={T.labelTight}
                    title={
                      f.checkedAt
                        ? `Probed ${formatDateTime(timestampDate(f.checkedAt))}`
                        : "Never probed"
                    }
                  >
                    {SUPPORT_LABELS[f.support]}
                  </Badge>
                </TableCell>
                <TableCell className="hidden @3xl:table-cell text-xs whitespace-normal text-muted-foreground">
                  {f.message || "—"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}
