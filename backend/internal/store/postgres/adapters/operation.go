package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// OperationRepo satisfies operation.Repository plus the worker-side
// ClaimNext extension used by the operations runner.
type OperationRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

// NewOperationRepo constructs an OperationRepo. The pool argument is
// used by ClaimNext (raw SQL with FOR UPDATE SKIP LOCKED — not exposed
// through sqlc because the locking semantics matter and stay clearer
// in raw SQL). Pass nil only in test paths that don't exercise the
// runner.
func NewOperationRepo(q *sqlc.Queries, pool *pgxpool.Pool) *OperationRepo {
	return &OperationRepo{q: q, pool: pool}
}

// ClaimNext atomically picks the oldest PENDING operation and flips
// it to RUNNING. Returns operation.ErrNoOperationToClaim when nothing
// is pending — workers loop on a ticker and this is the cheap
// no-op signal.
//
// SQL pattern: `SELECT ... FOR UPDATE SKIP LOCKED LIMIT 1` inside an
// UPDATE so concurrent workers grab disjoint rows. SKIP LOCKED is the
// load-bearing piece; without it two workers would block each other
// instead of getting separate rows.
func (r *OperationRepo) ClaimNext(ctx context.Context) (operation.Operation, error) {
	if r.pool == nil {
		return operation.Operation{}, errors.New("operation: pool unavailable on this OperationRepo")
	}
	const stmt = `
UPDATE operations
SET    state = 'RUNNING', updated_at = NOW()
WHERE  operation_id = (
    SELECT operation_id FROM operations
    WHERE  state = 'PENDING'
    ORDER  BY created_at ASC
    LIMIT  1
    FOR    UPDATE SKIP LOCKED
)
RETURNING operation_id, tenant_id, type, state, metadata, response,
          error_code, error_message, created_at, updated_at, done_at;
`
	var (
		opID, tenantID   uuid.UUID
		opType           string
		state            string
		metadata         []byte
		response         []byte
		errCode, errMsg  *string
		createdAt, updAt time.Time
		doneAt           *time.Time
	)
	err := r.pool.QueryRow(ctx, stmt).Scan(
		&opID, &tenantID, &opType, &state, &metadata, &response,
		&errCode, &errMsg, &createdAt, &updAt, &doneAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return operation.Operation{}, operation.ErrNoOperationToClaim
		}
		return operation.Operation{}, fmt.Errorf("operation: claim: %w", err)
	}
	out := operation.Operation{
		OperationID: opID,
		TenantID:    tenantID,
		Type:        opType,
		State:       operation.State(state),
		Metadata:    metadata,
		Response:    response,
		CreatedAt:   createdAt,
		UpdatedAt:   updAt,
		DoneAt:      doneAt,
	}
	if errCode != nil {
		out.ErrorCode = *errCode
	}
	if errMsg != nil {
		out.ErrorMessage = *errMsg
	}
	return out, nil
}

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
		OperationID:  uuidFrom(o.ID),
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
