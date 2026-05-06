package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// OperationRepo satisfies operation.Repository.
type OperationRepo struct {
	q *sqlc.Queries
}

func NewOperationRepo(q *sqlc.Queries) *OperationRepo { return &OperationRepo{q: q} }

var _ operation.Repository = (*OperationRepo)(nil)

func (r *OperationRepo) Create(ctx context.Context, op operation.Operation) error {
	return r.q.CreateOperation(ctx,
		pgUUID(op.OperationID),
		pgUUID(op.TenantID),
		op.Type,
		sqlc.OperationState(string(op.State)),
		op.Metadata,
	)
}

func (r *OperationRepo) Get(ctx context.Context, opID, tenantID uuid.UUID) (operation.Operation, error) {
	row, err := r.q.GetOperation(ctx, pgUUID(opID), pgUUID(tenantID))
	if err != nil {
		return operation.Operation{}, err
	}
	return operationFromSQLC(row.Operation), nil
}

func (r *OperationRepo) UpdateState(ctx context.Context, opID uuid.UUID, newState operation.State, metadata, response []byte, errCode, errMsg string) error {
	rows, err := r.q.UpdateOperationState(ctx,
		pgUUID(opID),
		sqlc.OperationState(string(newState)),
		metadata,
		response,
		strPtr(errCode),
		strPtr(errMsg),
	)
	if err != nil {
		return fmt.Errorf("update operation: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("operation %s not found", opID)
	}
	return nil
}

func (r *OperationRepo) Cancel(ctx context.Context, opID, tenantID uuid.UUID) error {
	rows, err := r.q.CancelOperation(ctx, pgUUID(opID), pgUUID(tenantID))
	if err != nil {
		return fmt.Errorf("cancel operation: %w", err)
	}
	if rows == 0 {
		return operation.ErrNotCancellable
	}
	return nil
}

func (r *OperationRepo) List(ctx context.Context, tenantID uuid.UUID, state *operation.State, afterID uuid.UUID, pageSize int32) ([]operation.Operation, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	var ns sqlc.NullOperationState
	if state != nil {
		ns = sqlc.NullOperationState{OperationState: sqlc.OperationState(string(*state)), Valid: true}
	}
	rows, err := r.q.ListOperations(ctx, pgUUID(tenantID), ns, pgUUID(afterID), pageSize)
	if err != nil {
		return nil, "", fmt.Errorf("list operations: %w", err)
	}
	out := make([]operation.Operation, 0, len(rows))
	for _, row := range rows {
		out = append(out, operationFromSQLC(row.Operation))
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		next = out[len(out)-1].OperationID.String()
	}
	return out, next, nil
}

// PurgeTerminalBefore deletes operations in a terminal state whose `done_at`
// is older than `cutoff`. Bounded at 10k rows per call (sqlc query); the
// reaper worker loops until 0 to drain a backlog without pinning locks.
func (r *OperationRepo) PurgeTerminalBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	ts := pgtype.Timestamptz{Time: cutoff, Valid: true}
	return r.q.PurgeTerminalOperations(ctx, ts)
}

func operationFromSQLC(o sqlc.Operation) operation.Operation {
	return operation.Operation{
		OperationID:  uuidFrom(o.OperationID),
		TenantID:     uuidFrom(o.TenantID),
		Type:         o.Type,
		State:        operation.State(string(o.State)),
		Metadata:     o.Metadata,
		Response:     o.Response,
		ErrorCode:    derefStr(o.ErrorCode),
		ErrorMessage: derefStr(o.ErrorMessage),
		CreatedAt:    timeFrom(o.CreatedAt),
		UpdatedAt:    timeFrom(o.UpdatedAt),
		DoneAt:       timePtr(o.DoneAt),
	}
}
