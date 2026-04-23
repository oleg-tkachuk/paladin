package connectshim

import (
	"context"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/presign"
)

type PresignServer struct {
	paladinv1connect.UnimplementedPresignServiceHandler
	H *presign.Handler
}

func NewPresignServer(h *presign.Handler) *PresignServer { return &PresignServer{H: h} }

func (s *PresignServer) RegenerateUploadUrl(ctx context.Context, req *connect.Request[pb.RegenerateUploadUrlRequest]) (*connect.Response[pb.RegenerateUploadUrlResponse], error) {
	ttl := req.Msg.GetTtl().AsDuration()
	// PresignPut on the handler takes contentType/checksumAlgo which the proto
	// RegenerateUploadUrl doesn't carry — leave empty, handler enforces Cedar
	// against the stored object's metadata (the check doesn't depend on these).
	url, headers, expiresAt, err := s.H.PresignPut(ctx, req.Msg.GetName(), "", "", ttl, 0)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.RegenerateUploadUrlResponse{
		UploadUrl: &pb.PresignedUrl{
			Url:             url,
			Method:          "PUT",
			RequiredHeaders: headers,
			ExpiresAt:       tsProto(expiresAt),
		},
	}), nil
}

func (s *PresignServer) PresignDownload(ctx context.Context, req *connect.Request[pb.PresignDownloadRequest]) (*connect.Response[pb.PresignDownloadResponse], error) {
	ttl := req.Msg.GetTtl().AsDuration()
	url, headers, expiresAt, err := s.H.PresignGet(ctx, req.Msg.GetName(), ttl, req.Msg.GetContentDisposition())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.PresignDownloadResponse{
		DownloadUrl: &pb.PresignedUrl{
			Url:             url,
			Method:          "GET",
			RequiredHeaders: headers,
			ExpiresAt:       tsProto(expiresAt),
		},
	}), nil
}

var _ paladinv1connect.PresignServiceHandler = (*PresignServer)(nil)
