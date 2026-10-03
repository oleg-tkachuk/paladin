package data

import (
	"context"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/presignh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

type PresignServer struct {
	paladindatav1connect.UnimplementedPresignServiceHandler
	H presignHandler
}

func NewPresignServer(h *presignh.Handler) *PresignServer { return &PresignServer{H: h} }

func (s *PresignServer) RegenerateUploadUrl(ctx context.Context, req *connect.Request[pb.RegenerateUploadUrlRequest]) (*connect.Response[pb.RegenerateUploadUrlResponse], error) {
	m := req.Msg
	collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.RegenerateUploadURL(ctx, collection, objectID, m.GetTtl().AsDuration())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.RegenerateUploadUrlResponse{
		UploadUrl:      presignedUrlProto(out.URL, "PUT", out.Headers, out.ExpiresAt, "", nil),
		CompletionMode: completionModeProto(out.CompletionMode),
	}), nil
}

func (s *PresignServer) PresignDownload(ctx context.Context, req *connect.Request[pb.PresignDownloadRequest]) (*connect.Response[pb.PresignDownloadResponse], error) {
	m := req.Msg
	collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	url, headers, expires, err := s.H.PresignGet(ctx, collection, objectID, m.GetTtl().AsDuration(), m.GetContentDisposition(), m.GetRequireEtagMatch())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.PresignDownloadResponse{
		DownloadUrl: presignedUrlProto(url, "GET", headers, expires, "", nil),
	}), nil
}

var _ paladindatav1connect.PresignServiceHandler = (*PresignServer)(nil)
