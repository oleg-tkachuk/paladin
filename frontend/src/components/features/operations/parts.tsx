"use client";

// The pieces every view of an operation shares — the drawer in the top bar
// and the operations page: its collection as a link, its state's icon, and
// its whole record, in a dialog from the drawer and in a panel beside the
// page's list.

import { useCallback, useState } from "react";
import Link from "next/link";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ClockIcon,
  NoSymbolIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { errorMessage } from "@/hooks/errorContract";
import {
  RecordBlock,
  RecordDialog,
  RecordField,
  RecordFields,
} from "@/components/ui/RecordDialog";
import { DetailPanel } from "@/components/ui/DetailPanel";
import { Timestamp } from "@/components/Timestamp";
import { ActorName } from "@/components/features/audit/ActorName";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import { formatDuration } from "@/lib/format/duration";
import { collectionHref } from "@/lib/navigation/hrefs";
import {
  OP_STATUS_BADGE,
  isActive,
  objectCountLabel,
  operationElapsedMs,
  operationError,
  operationLabel,
  operationOutcome,
  operationProgress,
  operationStatus,
  parseCollectionName,
  progressPercent,
  type OpStatus,
} from "@/lib/operations";
import type { OpProgress } from "@/lib/operationProgress";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

export const STATUS_ICON = {
  pending: ClockIcon,
  running: ArrowPathIcon,
  succeeded: CheckCircleIcon,
  failed: XCircleIcon,
  cancelled: NoSymbolIcon,
} as const;
export const STATUS_TINT: Record<OpStatus, string> = {
  pending: "text-muted-foreground",
  running: "text-warning",
  succeeded: "text-success",
  failed: "text-destructive",
  cancelled: "text-muted-foreground",
};

/** The collection an operation acts on, as a link that closes the drawer. */
export function CollectionLink({
  name,
  onNavigate,
}: {
  name: string;
  onNavigate?: () => void;
}) {
  const c = parseCollectionName(name);
  if (!c) return <span className={T.code}>{name}</span>;
  return (
    <Link
      href={collectionHref(c.tenant, c.collection)}
      onClick={onNavigate}
      className={cn(T.code, "break-all hover:text-primary hover:underline")}
      title={name}
    >
      {c.collection}
    </Link>
  );
}

/** An operation's whole record: what it was asked, how it went, why not. */
function OperationFields({ op }: { op: Operation }) {
  const status = operationStatus(op);
  const outcome = operationOutcome(op);
  const progress = operationProgress(op);
  const elapsed = operationElapsedMs(op, new Date());
  return (
    <RecordFields>
      <RecordField label="State">
        <Badge variant={OP_STATUS_BADGE[status].variant}>
          {OP_STATUS_BADGE[status].label}
        </Badge>
      </RecordField>
      <RecordField label="Type" mono>
        {op.type}
      </RecordField>
      {op.target ? (
        <>
          <RecordField label="Collection">
            <CollectionLink name={op.target.collection} />
          </RecordField>
          {op.target.destination ? (
            <RecordField label="Copied into">
              <CollectionLink name={op.target.destination} />
            </RecordField>
          ) : null}
          <RecordField label="Objects">
            {objectCountLabel(op.target.objectCount)}
          </RecordField>
        </>
      ) : null}
      {op.initiatorSubject ? (
        <RecordField label="Started by">
          <ActorName
            subject={op.initiatorSubject}
            tenantId={op.initiatorTenantId}
          />
        </RecordField>
      ) : null}
      <RecordField label="Started">
        {op.createdAt ? <Timestamp ts={op.createdAt} /> : "—"}
      </RecordField>
      {op.doneAt ? (
        <RecordField label="Finished">
          <Timestamp ts={op.doneAt} />
        </RecordField>
      ) : null}
      {op.expireTime ? (
        // The housekeeping reaper removes finished operations; until then
        // they stay listed, cleared or not.
        <RecordField label="Kept until">
          <Timestamp ts={op.expireTime} />
        </RecordField>
      ) : null}
      {elapsed !== null ? (
        <RecordField label={isActive(status) ? "Running for" : "Took"}>
          {formatDuration(elapsed)}
        </RecordField>
      ) : null}
      {progress ? (
        <RecordField label="Progress">
          {progress.processed} of {progress.total}
        </RecordField>
      ) : null}
      {outcome ? (
        <RecordField label="Result">
          {outcome.succeeded} done
          {outcome.alreadyApplied > 0
            ? `, ${outcome.alreadyApplied} already so`
            : ""}
          {outcome.failed > 0 ? `, ${outcome.failed} failed` : ""} of{" "}
          {outcome.total}
        </RecordField>
      ) : null}
      {operationError(op) ? (
        <RecordField label="Error">
          <span className="text-destructive">{operationError(op)}</span>
        </RecordField>
      ) : null}
      {outcome && outcome.failures.length > 0 ? (
        <RecordBlock label="Objects that failed">
          {outcome.failures.map((f) => `${f.objectId}  ${f.reason}`).join("\n")}
        </RecordBlock>
      ) : null}
    </RecordFields>
  );
}

/**
 * An operation's record in a dialog, for the drawer in the top bar, which is
 * itself a panel and has no list beside it to keep in view.
 */
export function OperationRecord({
  op,
  onClose,
}: {
  op: Operation | null;
  onClose: () => void;
}) {
  return (
    <RecordDialog
      open={op !== null}
      onOpenChange={(o) => !o && onClose()}
      title={op ? operationLabel(op) : "Operation"}
      subtitle={op?.name}
    >
      {op ? <OperationFields op={op} /> : null}
    </RecordDialog>
  );
}

/** An operation's record in a panel beside the operations list. */
export function OperationPanel({
  op,
  onClose,
}: {
  op: Operation | null;
  onClose: () => void;
}) {
  return (
    <DetailPanel
      open={op !== null}
      onClose={onClose}
      rowId={op?.name}
      title={op ? operationLabel(op) : "Operation"}
      identifier={op?.name}
    >
      {op ? <OperationFields op={op} /> : null}
    </DetailPanel>
  );
}

/** How far a running operation has got, as a bar and its count. */
export function OperationProgressBar({ progress }: { progress: OpProgress }) {
  return (
    <div className="space-y-0.5">
      <div
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={progress.total}
        aria-valuenow={progress.processed}
        className="h-1.5 overflow-hidden rounded-full bg-muted"
      >
        <div
          className="h-full rounded-full bg-warning transition-[width]"
          style={{ width: `${progressPercent(progress)}%` }}
        />
      </div>
      <p className={cn(T.hint, "tabular-nums")}>
        {progress.processed} of {progress.total} processed
      </p>
    </div>
  );
}

/**
 * Stops a running operation, once asked twice. A batch stopped part-way is
 * not undone by starting it again: the objects it already moved or deleted
 * stay moved or deleted, so one click on a small button must not do it.
 * onError receives why the cancel failed, or null when a new one starts.
 */
export function CancelOperationButton({
  op,
  onCancel,
  onError,
}: {
  op: Operation;
  onCancel: (name: string) => Promise<void>;
  onError: (message: string | null) => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const [canceling, setCanceling] = useState(false);
  const cancel = useCallback(async () => {
    setCanceling(true);
    onError(null);
    try {
      await onCancel(op.name);
    } catch (err) {
      onError(errorMessage(err, "Cancel failed"));
    } finally {
      setCanceling(false);
    }
  }, [onCancel, onError, op.name]);
  return (
    <>
      <Button
        variant="outline"
        size="sm"
        className="h-7 shrink-0 px-2 text-xs"
        disabled={canceling}
        onClick={() => setConfirming(true)}
      >
        {canceling ? "Canceling…" : "Cancel"}
      </Button>
      <ConfirmModal
        isOpen={confirming}
        onClose={() => setConfirming(false)}
        onConfirm={cancel}
        title="Cancel this operation?"
        message={`${operationLabel(op)} stops where it is. What it has already done is not undone.`}
        confirmText="Cancel operation"
        cancelText="Keep running"
        loading={canceling}
      />
    </>
  );
}
