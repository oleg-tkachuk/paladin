package admin

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/audith"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
)

type AuditServer struct {
	paladinadminv1connect.UnimplementedAuditLogServiceHandler
	H *audith.Handler
}

func NewAuditServer(h *audith.Handler) *AuditServer { return &AuditServer{H: h} }

func (s *AuditServer) ListAuditLog(ctx context.Context, req *connect.Request[pb.ListAuditLogRequest]) (*connect.Response[pb.ListAuditLogResponse], error) {
	m := req.Msg
	args := admindomain.ListAuditArgs{PageSize: m.GetPage().GetPageSize()}
	if tok := m.GetPage().GetPageToken(); tok != "" {
		args.AfterAt, args.AfterID = decodeAuditCursor(tok)
	}
	// Filter is CEL; keep server-side post-filter for slice 2 — TODO compile CEL.
	_ = m.GetFilter()
	list, next, err := s.H.ListAuditLog(ctx, args)
	if err != nil {
		return nil, err
	}
	out := &pb.ListAuditLogResponse{Page: pageResponseProto(next)}
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
	if err := s.H.ExportAuditLog(ctx, req.Msg.GetFilter(), req.Msg.GetDestination()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.Operation{}), nil
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
