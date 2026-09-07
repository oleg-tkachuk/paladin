// Package celh implements the admin CELService — a stateless, no-DB
// validator the admin UI calls while operators type CEL filter / match
// expressions for lifecycle rules, event subscriptions, and list-RPC
// query strings.
//
// Trust posture: identical to PolicyService.Validate (Cedar). The RPC
// is non-mutating, exposes no tenant data, and the schema set is
// hard-coded server-side. The admin interceptor stack still gates on
// the admin audience JWT — anonymous callers cannot reach this RPC.
package celh

import (
	"context"
	"errors"
	"fmt"
	"github.com/oleg-tkachuk/paladin/internal/safecast"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	celfilter "github.com/oleg-tkachuk/paladin/internal/filter/cel"
)

// Handler implements paladinadminv1connect.CELServiceHandler.
//
// Stateless: all dependencies are pulled from the cel package directly.
// No constructor arguments needed; the type stays a struct (not a bare
// function) so we can attach the connect handler interface without
// cluttering the package namespace.
type Handler struct {
	paladinadminv1connect.UnimplementedCELServiceHandler
}

func NewHandler() *Handler { return &Handler{} }

// Validate type-checks the expression against the named schema and
// returns the first compile diagnostic (with line/column when cel-go
// supplies position info) or `valid: true`.
//
// Empty expression → valid (the match-all sentinel). Unknown schema →
// InvalidArgument.
func (h *Handler) Validate(ctx context.Context, req *connect.Request[pb.ValidateCELRequest]) (*connect.Response[pb.ValidateCELResponse], error) {
	m := req.Msg
	schema := celfilter.SchemaByName(m.GetSchema())
	if schema == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("unknown schema %q (want one of: Object, Collection, AuditLogEntry, EventEnvelope)", m.GetSchema()))
	}

	err := celfilter.CompileFirstError(schema, m.GetExpression())
	if err == nil {
		return connect.NewResponse(&pb.ValidateCELResponse{Valid: true}), nil
	}

	var ce *celfilter.CompileError
	if errors.As(err, &ce) {
		return connect.NewResponse(&pb.ValidateCELResponse{
			Valid:   false,
			Message: ce.Message,
			Line:    safecast.Int32(ce.Line),
			Column:  safecast.Int32(ce.Column),
		}), nil
	}
	// Defence in depth — CompileFirstError always returns *CompileError
	// or nil today, but keep a fallback so a future refactor doesn't
	// silently surface a bare error string with no position info.
	return connect.NewResponse(&pb.ValidateCELResponse{
		Valid:   false,
		Message: err.Error(),
	}), nil
}

var _ paladinadminv1connect.CELServiceHandler = (*Handler)(nil)
