package admin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/audith"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

type AuditServer struct {
	paladinadminv1connect.UnimplementedAuditLogServiceHandler
	H auditHandler
}

func NewAuditServer(h *audith.Handler) *AuditServer { return &AuditServer{H: h} }

func (s *AuditServer) ListAuditLog(ctx context.Context, req *pb.ListAuditLogRequest) (*pb.ListAuditLogResponse, error) {
	m := req
	args := admindomain.ListAuditArgs{
		PageSize:  m.GetPage().GetPageSize(),
		PageToken: m.GetPage().GetPageToken(),
		Order:     auditOrderOf(m.GetOrderBy(), m.GetSortOrder()),
		Facets:    auditFacetsFromProto(m.GetFacets()),
	}
	if id := m.GetTenantId(); id != "" {
		tenant, err := uuid.Parse(id)
		if err != nil {
			return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
		}
		args.TrailTenantID = tenant
	}
	list, next, err := s.H.ListAuditLog(ctx, args, m.GetFilter())
	if err != nil {
		return nil, err
	}
	out := &pb.ListAuditLogResponse{Page: convx.PageResponseProto(next)}
	for i := range list {
		out.Entries = append(out.Entries, auditEntryToProto(&list[i]))
	}
	// Counted with the first page only: a later page is the same listing.
	if m.GetCountFacets() && args.PageToken == "" {
		counts, err := s.H.CountAuditFacets(ctx, args, m.GetFilter())
		if err != nil {
			return nil, err
		}
		out.FacetCounts = auditFacetCountsToProto(counts)
	}
	return out, nil
}

// auditOutcomes names each outcome on the wire.
var auditOutcomes = map[admindomain.AuditOutcome]pb.AuditOutcome{
	admindomain.AuditOutcomeOK:    pb.AuditOutcome_AUDIT_OUTCOME_OK,
	admindomain.AuditOutcomeError: pb.AuditOutcome_AUDIT_OUTCOME_ERROR,
}

// auditFacetsFromProto is f in the domain's terms; an outcome the domain
// does not know is left out.
func auditFacetsFromProto(f *pb.AuditFacets) admindomain.AuditFacets {
	out := admindomain.AuditFacets{Planes: f.GetPlanes(), Services: f.GetServices()}
	for _, o := range f.GetOutcomes() {
		for domain, wire := range auditOutcomes {
			if wire == o {
				out.Outcomes = append(out.Outcomes, domain)
			}
		}
	}
	return out
}

// auditFacetCountsToProto is counts on the wire: an outcome by its
// AuditOutcome name, as AuditFacets names it.
func auditFacetCountsToProto(counts []admindomain.AuditFacetCount) []*commonpb.FacetCount {
	out := make([]*commonpb.FacetCount, 0, len(counts))
	for _, c := range counts {
		value := c.Value
		if c.Facet == admindomain.AuditFacetOutcome {
			value = auditOutcomes[admindomain.AuditOutcome(c.Value)].String()
		}
		out = append(out, &commonpb.FacetCount{Facet: c.Facet, Value: value, Count: c.Count})
	}
	return out
}

func (s *AuditServer) GetAuditLogEntry(ctx context.Context, req *pb.GetAuditLogEntryRequest) (*pb.AuditLogEntry, error) {
	id, err := uuid.Parse(req.GetEntryId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	out, err := s.H.GetAuditLogEntry(ctx, id)
	if err != nil {
		return nil, err
	}
	return auditEntryToProto(out), nil
}

func (s *AuditServer) ExportAuditLog(ctx context.Context, req *pb.ExportAuditLogRequest) (*pb.Operation, error) {
	res, err := s.H.ExportAuditLog(ctx, req.GetFilter(), req.GetDestination())
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(res)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, fmt.Errorf("export: marshal result: %w", err))
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
	return op, nil
}

var _ paladinadminv1connect.AuditLogServiceHandler = (*AuditServer)(nil)

// auditOrderOf is the order a request names. A named field is ascending
// unless DESC is asked for; no field is newest first unless ASC is, which is
// the order the log was always read in.
func auditOrderOf(orderBy string, dir commonpb.SortOrder) keyset.Order {
	if orderBy == "" {
		if dir == commonpb.SortOrder_SORT_ORDER_ASC {
			return keyset.Order{Field: admindomain.AuditOrderByAt}
		}
		// The handler reads no order as admindomain.DefaultAuditOrder.
		return keyset.Order{}
	}
	return keyset.Order{Field: orderBy, Desc: dir == commonpb.SortOrder_SORT_ORDER_DESC}
}
