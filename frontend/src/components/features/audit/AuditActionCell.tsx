import { ExclamationTriangleIcon } from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { type ActionKind, parseAuditAction, parseAuditError } from "./action";

// The verb is a dot beside the name; red on the row is kept for failures, so
// a successful delete does not read like a failed one.
const KIND_DOT: Record<ActionKind, string> = {
  destroy: "bg-destructive",
  change: "bg-warning",
  create: "bg-success",
  read: "bg-info",
  other: "bg-muted-foreground",
};

/** Label for a failure whose message names no Connect code. */
const FAILED = "failed";

/**
 * An audit entry's action: the method in front, where it ran beneath, and the
 * outcome when it failed. The full action and error stay in the tooltip and
 * the entry's detail dialog.
 */
export function AuditActionCell({
  action,
  errorMessage,
}: {
  action: string;
  errorMessage?: string;
}) {
  const a = parseAuditAction(action);
  const err = errorMessage ? parseAuditError(errorMessage) : undefined;
  return (
    <div className="min-w-0 space-y-0.5" title={action}>
      <div className="flex min-w-0 items-center gap-2">
        <span
          aria-hidden
          data-kind={a.kind}
          className={cn(T.pillDot, "shrink-0", KIND_DOT[a.kind])}
        />
        <span className={cn(T.code, "truncate font-medium")}>{a.name}</span>
        {err && (
          <Badge variant="destructive" className="shrink-0">
            {err.code ?? FAILED}
          </Badge>
        )}
      </div>
      {a.scope && <div className={cn(T.hint, "truncate pl-4")}>{a.scope}</div>}
      {err && (
        <div
          className="flex items-start gap-1.5 pl-4 text-xs text-destructive"
          title={errorMessage}
        >
          <ExclamationTriangleIcon className="mt-0.5 size-3 shrink-0" />
          <span className="line-clamp-2 break-words">{err.message}</span>
        </div>
      )}
    </div>
  );
}
