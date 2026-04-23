package connectshim

import (
	"context"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/policy"
)

type PolicyServer struct {
	paladinv1connect.UnimplementedPolicyServiceHandler
	H *policy.Handler
}

func NewPolicyServer(h *policy.Handler) *PolicyServer { return &PolicyServer{H: h} }

func (s *PolicyServer) ValidatePolicy(ctx context.Context, req *connect.Request[pb.ValidatePolicyRequest]) (*connect.Response[pb.ValidatePolicyResponse], error) {
	ok, errMsg := s.H.ValidatePolicy(ctx, req.Msg.GetPolicy())
	return connect.NewResponse(&pb.ValidatePolicyResponse{Valid: ok, Error: errMsg}), nil
}

var _ paladinv1connect.PolicyServiceHandler = (*PolicyServer)(nil)
