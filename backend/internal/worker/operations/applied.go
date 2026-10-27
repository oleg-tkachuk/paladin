package operations

import (
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
)

// A batch whose worker died is failed by the stale-operation reclaimer with
// only a lower bound on how far it got, and the caller's recovery is to issue
// the same batch again. That is only safe to tell them if the second run counts
// an item the first one already applied as done rather than as a failure — so
// each executor recognises its own finished work and reports it under
// already_applied, which Succeeded includes.
//
// The reasons below are what the worker logs for such an item.
const (
	reasonAlreadyCopied   = "destination already holds the source's content"
	reasonAlreadyDeleted  = "object is already in the trash"
	reasonAlreadyErased   = "object was already permanently deleted"
	reasonAlreadyRestored = "object is already available"
)

// batchLogger keeps an executor without a configured logger silent.
func batchLogger(l *zap.Logger) *zap.Logger {
	if l == nil {
		return zap.NewNop()
	}
	return l
}

// logAlreadyApplied records an item a batch found already done. Debug: a
// reissue of a large batch finds most of it done, and one line per item would
// flood the log; logBatchOutcome reports the count at Info.
func logAlreadyApplied(l *zap.Logger, op operationh.Operation, objectID uuid.UUID, reason string) {
	batchLogger(l).Debug("batch item already applied; counted as succeeded",
		zap.String("operation_id", op.OperationID.String()),
		zap.String("type", op.Type),
		zap.String("object_id", objectID.String()),
		zap.String("reason", reason))
}

// logBatchOutcome records how a batch ended, in aggregate.
func logBatchOutcome(l *zap.Logger, op operationh.Operation, total, succeeded, alreadyApplied, failed int) {
	batchLogger(l).Info("batch finished",
		zap.String("operation_id", op.OperationID.String()),
		zap.String("type", op.Type),
		zap.Int("total", total),
		zap.Int("succeeded", succeeded),
		zap.Int("already_applied", alreadyApplied),
		zap.Int("failed", failed))
}

// logAdopted records a destination row a batch copy took over from the dead
// run that created it. Info: unlike an item found done, this one is written
// now, and the row's history is otherwise lost.
func logAdopted(l *zap.Logger, op operationh.Operation, srcID, dstID, creator uuid.UUID) {
	batchLogger(l).Info("batch copy adopted a pending destination left by a stopped operation",
		zap.String("operation_id", op.OperationID.String()),
		zap.String("object_id", srcID.String()),
		zap.String("destination_id", dstID.String()),
		zap.String("creator_operation_id", creator.String()))
}
