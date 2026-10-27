package adapters

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
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
func (r *OperationRepo) ClaimNext(ctx context.Context) (operationh.Operation, error) {
	if r.pool == nil {
		return operationh.Operation{}, errors.New("operation: pool unavailable on this OperationRepo")
	}
	const stmt = `
UPDATE operations
SET    state = 'RUNNING', updated_at = NOW()
WHERE  id = (
    SELECT id FROM operations
    WHERE  state = 'PENDING'
    -- A tenant in the trash is frozen: its operations wait, PENDING, for a
    -- restore.
    AND    NOT EXISTS (SELECT 1 FROM tenants t
                        WHERE t.id = operations.tenant_id AND t.deleted_at IS NOT NULL)
    ORDER  BY created_at ASC
    LIMIT  1
    FOR    UPDATE SKIP LOCKED
)
RETURNING id, tenant_id, type, state, metadata, response,
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
			return operationh.Operation{}, operationh.ErrNoOperationToClaim
		}
		return operationh.Operation{}, fmt.Errorf("operation: claim: %w", err)
	}
	out := operationh.Operation{
		OperationID: opID,
		TenantID:    tenantID,
		Type:        opType,
		State:       operationh.State(state),
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

var _ operationh.Repository = (*OperationRepo)(nil)

func (r *OperationRepo) Create(ctx context.Context, op operationh.Operation) error {
	return r.q.CreateOperation(ctx,
		pgUUID(op.OperationID),
		pgUUID(op.TenantID),
		op.Type,
		sqlc.OperationState(string(op.State)),
		op.Metadata,
		op.InitiatorSubject,
	)
}

func (r *OperationRepo) Get(ctx context.Context, opID, tenantID uuid.UUID) (operationh.Operation, error) {
	row, err := r.q.GetOperation(ctx, pgUUID(opID), pgUUID(tenantID))
	if err != nil {
		return operationh.Operation{}, err
	}
	return operationFromSQLC(row.Operation), nil
}

func (r *OperationRepo) UpdateState(ctx context.Context, opID uuid.UUID, newState operationh.State, response []byte, failedCount int64, errCode, errMsg string) error {
	rows, err := r.q.UpdateOperationState(ctx,
		pgUUID(opID),
		sqlc.OperationState(string(newState)),
		response,
		strPtr(errCode),
		strPtr(errMsg),
		failedCount,
	)
	if err != nil {
		return fmt.Errorf("update operation: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("operation %s: %w", opID, operationh.ErrOperationFinished)
	}
	return nil
}

func (r *OperationRepo) ReportProgress(ctx context.Context, opID uuid.UUID, progress []byte) error {
	rows, err := r.q.ReportOperationProgress(ctx, pgUUID(opID), progress)
	if err != nil {
		return fmt.Errorf("report operation progress: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("operation %s: %w", opID, operationh.ErrOperationFinished)
	}
	return nil
}

// IsLive reports whether the operation is PENDING or RUNNING. A purged one is
// not: only terminal operations are purged.
func (r *OperationRepo) IsLive(ctx context.Context, opID uuid.UUID) (bool, error) {
	live, err := r.q.OperationIsLive(ctx, pgUUID(opID))
	if err != nil {
		return false, fmt.Errorf("operation liveness: %w", err)
	}
	return live, nil
}

func (r *OperationRepo) Cancel(ctx context.Context, opID, tenantID uuid.UUID) error {
	rows, err := r.q.CancelOperation(ctx, pgUUID(opID), pgUUID(tenantID))
	if err != nil {
		return fmt.Errorf("cancel operation: %w", err)
	}
	if rows == 0 {
		return operationh.ErrNotCancellable
	}
	return nil
}

// Dismiss implements operationh.Repository.
func (r *OperationRepo) Dismiss(ctx context.Context, tenantID uuid.UUID, initiator string, ids []uuid.UUID) (int64, error) {
	var pgIDs []pgtype.UUID
	if len(ids) > 0 {
		pgIDs = make([]pgtype.UUID, len(ids))
		for i, id := range ids {
			pgIDs[i] = pgUUID(id)
		}
	}
	n, err := r.q.DismissOperations(ctx, pgUUID(tenantID), initiator, pgIDs)
	if err != nil {
		return 0, fmt.Errorf("dismiss operations: %w", err)
	}
	return n, nil
}

// operationPredicates are ListOperations' predicates as the queries take
// them: the filter's pushdown hints, narrowed by the facets.
type operationPredicates struct {
	typeIn, errorCodeIn, stateIn []string
	typeLike, errorMessageNeq    *string
	done, hasFailures            *bool
	createdGTE, createdLTE       pgtype.Timestamptz
}

// predicatesOf is args' predicates. Pushdown: see admin_bucket.go. The
// filter's `state` is compared as text in the query, so a literal the enum
// does not hold matches nothing rather than failing the cast.
func predicatesOf(args operationh.ListArgs) operationPredicates {
	pd := hints(cel.OperationSchema, args.Filter)
	var p operationPredicates
	p.typeIn, p.typeLike = pd.StringHint("type")
	p.errorCodeIn, _ = pd.StringHint("error_code")
	p.stateIn, _ = pd.StringHint("state")
	p.done = pd.BoolHint("done")
	p.hasFailures = pd.BoolHint("has_failures")
	// `error_message != ""` is how a caller asks for "operations that failed"
	// — the dashboard's failed-ops widget does exactly that. Without the hint
	// the predicate only narrowed the page it was handed, so a page of five
	// recent operations that happened to contain no failure rendered as "no
	// failures" while failures sat one page back.
	p.errorMessageNeq = pd.NeqHint("error_message")
	p.createdGTE, p.createdLTE = createdBounds(pd)
	if args.State != nil {
		p.stateIn = within(p.stateIn, stateNames([]operationh.State{*args.State}))
	}
	p.stateIn = within(p.stateIn, stateNames(args.Facets.States))
	p.typeIn = within(p.typeIn, args.Facets.Types)
	return p
}

// stateNames is states as the query compares them, nil for none.
func stateNames(states []operationh.State) []string {
	if len(states) == 0 {
		return nil
	}
	out := make([]string, len(states))
	for i, st := range states {
		out[i] = string(st)
	}
	return out
}

// within is the values both a and b allow, nil meaning any: b when a allows
// any, a when b does, otherwise their intersection — empty, not nil, when
// they share none, which matches nothing.
func within(a, b []string) []string {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	out := []string{}
	for _, v := range a {
		if slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	return out
}

func (r *OperationRepo) List(ctx context.Context, args operationh.ListArgs) ([]operationh.Operation, string, error) {
	tenantID, state, afterID, newestFirst, initiator := args.TenantID, args.State, args.AfterID, args.NewestFirst, args.Initiator
	pageSize := pageSizeOrDefault(args.PageSize)
	var ns *sqlc.OperationState
	if state != nil {
		v := sqlc.OperationState(string(*state))
		ns = &v
	}
	p := predicatesOf(args)

	// Two queries rather than one with a flipped comparison: the cursor test
	// has to move with the sort, and sqlc parameterises neither.
	out := make([]operationh.Operation, 0, pageSize)
	if newestFirst {
		rows, err := r.q.ListOperationsDesc(ctx, pgUUID(tenantID), ns, pgUUID(afterID), strPtrNonEmpty(initiator), args.HideDismissed,
			p.typeIn, p.typeLike, p.errorCodeIn, p.stateIn, p.done, p.hasFailures, p.errorMessageNeq,
			p.createdGTE, p.createdLTE, pageSize)
		if err != nil {
			return nil, "", fmt.Errorf("list operations: %w", err)
		}
		for _, row := range rows {
			out = append(out, operationFromSQLC(row.Operation))
		}
	} else {
		rows, err := r.q.ListOperations(ctx, pgUUID(tenantID), ns, pgUUID(afterID), strPtrNonEmpty(initiator), args.HideDismissed,
			p.typeIn, p.typeLike, p.errorCodeIn, p.stateIn, p.done, p.hasFailures, p.errorMessageNeq,
			p.createdGTE, p.createdLTE, pageSize)
		if err != nil {
			return nil, "", fmt.Errorf("list operations: %w", err)
		}
		for _, row := range rows {
			out = append(out, operationFromSQLC(row.Operation))
		}
	}
	var next string
	if len(out) == int(pageSize) && len(out) > 0 {
		next = out[len(out)-1].OperationID.String()
	}
	return out, next, nil
}

// CountFacets counts args' operations by state, without the state facet's
// own choices, and by type, without the type facet's.
func (r *OperationRepo) CountFacets(ctx context.Context, args operationh.ListArgs) ([]operationh.FacetCount, error) {
	byState := args
	byState.Facets.States = nil
	s := predicatesOf(byState)
	stateRows, err := r.q.CountOperationsByState(ctx, pgUUID(args.TenantID), strPtrNonEmpty(args.Initiator), args.HideDismissed,
		s.typeIn, s.typeLike, s.errorCodeIn, s.stateIn, s.done, s.hasFailures, s.errorMessageNeq, s.createdGTE, s.createdLTE)
	if err != nil {
		return nil, fmt.Errorf("count operations by state: %w", err)
	}
	byType := args
	byType.Facets.Types = nil
	t := predicatesOf(byType)
	typeRows, err := r.q.CountOperationsByType(ctx, pgUUID(args.TenantID), strPtrNonEmpty(args.Initiator), args.HideDismissed,
		t.typeIn, t.typeLike, t.errorCodeIn, t.stateIn, t.done, t.hasFailures, t.errorMessageNeq, t.createdGTE, t.createdLTE)
	if err != nil {
		return nil, fmt.Errorf("count operations by type: %w", err)
	}
	out := make([]operationh.FacetCount, 0, len(stateRows)+len(typeRows))
	for _, row := range stateRows {
		out = append(out, operationh.FacetCount{Facet: operationh.FacetState, Value: row.Value, Count: row.Count})
	}
	for _, row := range typeRows {
		out = append(out, operationh.FacetCount{Facet: operationh.FacetType, Value: row.Value, Count: row.Count})
	}
	return out, nil
}

// PurgeTerminalBefore deletes operations in a terminal state whose `done_at`
// is older than `cutoff`. Bounded at 10k rows per call (sqlc query); the
// reaper worker loops until 0 to drain a backlog without pinning locks.
func (r *OperationRepo) PurgeTerminalBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	ts := pgtype.Timestamptz{Time: cutoff, Valid: true}
	return r.q.PurgeTerminalOperations(ctx, ts)
}

// Touch keeps a RUNNING operation's updated_at fresh so the stale-operation
// reclaim can tell "still working" from "worker died". Bumps nothing else —
// metadata carries progress counters the executor writes concurrently.
func (r *OperationRepo) Touch(ctx context.Context, opID uuid.UUID) error {
	rows, err := r.q.TouchOperation(ctx, pgtype.UUID{Bytes: opID, Valid: true})
	if err != nil {
		return fmt.Errorf("operation: touch: %w", err)
	}
	if rows == 0 {
		// No longer RUNNING: cancelled, or reclaimed as lost.
		return fmt.Errorf("operation %s: %w", opID, operationh.ErrOperationFinished)
	}
	return nil
}

// ReclaimStale fails operations left RUNNING longer than staleAfter without a
// heartbeat. Returns how many were reclaimed.
func (r *OperationRepo) ReclaimStale(ctx context.Context, staleAfter time.Duration) (int64, error) {
	n, err := r.q.ReclaimStaleOperations(ctx, staleAfter.Microseconds())
	if err != nil {
		return 0, fmt.Errorf("operation: reclaim stale: %w", err)
	}
	return n, nil
}

func operationFromSQLC(o sqlc.Operation) operationh.Operation {
	return operationh.Operation{
		OperationID:      uuidFrom(o.ID),
		TenantID:         uuidFrom(o.TenantID),
		InitiatorSubject: o.InitiatorSubject,
		DismissedAt:      timePtr(o.DismissedAt),
		Type:             o.Type,
		State:            operationh.State(string(o.State)),
		Metadata:         o.Metadata,
		Progress:         o.Progress,
		Response:         o.Response,
		FailedCount:      o.FailedCount,
		ErrorCode:        derefStr(o.ErrorCode),
		ErrorMessage:     derefStr(o.ErrorMessage),
		CreatedAt:        timeFrom(o.CreatedAt),
		UpdatedAt:        timeFrom(o.UpdatedAt),
		DoneAt:           timePtr(o.DoneAt),
	}
}
