package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/api/connectshim/convx"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/audith"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
)

type AuditServer struct {
	paladinadminv1connect.UnimplementedAuditLogServiceHandler
	H auditHandler
}

func NewAuditServer(h *audith.Handler) *AuditServer { return &AuditServer{H: h} }

func (s *AuditServer) ListAuditLog(ctx context.Context, req *connect.Request[pb.ListAuditLogRequest]) (*connect.Response[pb.ListAuditLogResponse], error) {
	m := req.Msg
	args := admindomain.ListAuditArgs{PageSize: m.GetPage().GetPageSize()}
	if tok := m.GetPage().GetPageToken(); tok != "" {
		args.AfterAt, args.AfterID = decodeAuditCursor(tok)
	}
	list, next, err := s.H.ListAuditLog(ctx, args, m.GetFilter())
	if err != nil {
		return nil, err
	}
	out := &pb.ListAuditLogResponse{Page: convx.PageResponseProto(next)}
	for i := range list {
		out.Entries = append(out.Entries, auditEntryToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *AuditServer) GetAuditLogEntry(ctx context.Context, req *connect.Request[pb.GetAuditLogEntryRequest]) (*connect.Response[pb.AuditLogEntry], error) {
	id, err := uuid.Parse(req.Msg.GetEntryId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.GetAuditLogEntry(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(auditEntryToProto(out)), nil
}

func (s *AuditServer) ExportAuditLog(ctx context.Context, req *connect.Request[pb.ExportAuditLogRequest]) (*connect.Response[pb.Operation], error) {
	res, err := s.H.ExportAuditLog(ctx, req.Msg.GetFilter(), req.Msg.GetDestination())
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(res)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("export: marshal result: %w", err))
	}
	now := timestamppb.New(res.GeneratedAt)
	op := &pb.Operation{
		Name: "operations/audit-export-" + uuid.NewString(),
		Type: "paladin.admin.v1.AuditLogService/ExportAuditLog",
		Done: true,
		Result: &pb.Operation_Response{
			Response: &anypb.Any{
				TypeUrl: "type.googleapis.com/paladin.admin.v1.ExportAuditLogResult",
				Value:   payload,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	return connect.NewResponse(op), nil
}

var _ paladinadminv1connect.AuditLogServiceHandler = (*AuditServer)(nil)

func decodeAuditCursor(tok string) (time.Time, uuid.UUID) {
	idx := strings.LastIndex(tok, "/")
	if idx <= 0 {
		return time.Time{}, uuid.Nil
	}
	at, err := time.Parse(time.RFC3339Nano, tok[:idx])
	if err != nil {
		return time.Time{}, uuid.Nil
	}
	id, err := uuid.Parse(tok[idx+1:])
	if err != nil {
		return time.Time{}, uuid.Nil
	}
	return at, id
}
