package data

import (
	"context"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/pb/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/presign"
)

type PresignServer struct {
	paladindatav1connect.UnimplementedPresignServiceHandler
	H presignHandler
}

func NewPresignServer(h *presign.Handler) *PresignServer { return &PresignServer{H: h} }

func (s *PresignServer) RegenerateUploadUrl(ctx context.Context, req *connect.Request[pb.RegenerateUploadUrlRequest]) (*connect.Response[pb.RegenerateUploadUrlResponse], error) {
	m := req.Msg
	collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	// Regenerate uses PUT path; ContentType / ChecksumAlgo / SizeHint are
	// looked up server-side from the existing object row by the handler.
	url, headers, expires, err := s.H.PresignPut(ctx, collection, objectID, "", "", m.GetTtl().AsDuration(), 0)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.RegenerateUploadUrlResponse{
		UploadUrl: presignedUrlProto(url, "PUT", headers, expires, "", nil),
	}), nil
}

func (s *PresignServer) PresignDownload(ctx context.Context, req *connect.Request[pb.PresignDownloadRequest]) (*connect.Response[pb.PresignDownloadResponse], error) {
	m := req.Msg
	collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	url, headers, expires, err := s.H.PresignGet(ctx, collection, objectID, m.GetTtl().AsDuration(), m.GetContentDisposition())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.PresignDownloadResponse{
		DownloadUrl: presignedUrlProto(url, "GET", headers, expires, "", nil),
	}), nil
}

var _ paladindatav1connect.PresignServiceHandler = (*PresignServer)(nil)
