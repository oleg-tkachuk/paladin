package data

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	commonpb "github.com/oleg-tkachuk/paladin/internal/api/pb/common/v1"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
)

type MultipartServer struct {
	paladindatav1connect.UnimplementedMultipartUploadServiceHandler
	H *multipart.Handler
}

func NewMultipartServer(h *multipart.Handler) *MultipartServer { return &MultipartServer{H: h} }

func (s *MultipartServer) InitiateMultipartUpload(ctx context.Context, req *connect.Request[pb.InitiateMultipartUploadRequest]) (*connect.Response[pb.InitiateMultipartUploadResponse], error) {
	m := req.Msg
	objectKey, err := objectKeyNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	sess, err := s.H.InitiateMultipartUpload(ctx, multipart.InitiateArgs{
		ObjectKey:    objectKey,
		Key:          m.GetKey(),
		ContentType:  m.GetContentType(),
		SizeHint:     m.GetSizeBytes(),
		ChecksumAlgo: checksumAlgoStr(m.GetChecksumAlgorithm()),
		Metadata:     m.GetMetadata(),
		Tags:         m.GetTags(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.InitiateMultipartUploadResponse{
		// Object's full state is fetched lazily via GetObject; we surface the
		// minimal envelope here.
		Object: &pb.Object{
			ObjectId:  sess.ObjectID.String(),
			TenantId:  sess.TenantID.String(),
			ObjectKey: sess.ObjectKey,
			Key:       sess.Key,
		},
		UploadId:            sess.UploadID,
		RecommendedPartSize: sess.PartSizeBytes,
		TotalParts:          sess.TotalParts,
	}), nil
}

func (s *MultipartServer) PresignPart(ctx context.Context, req *connect.Request[pb.PresignPartRequest]) (*connect.Response[pb.PresignPartResponse], error) {
	m := req.Msg
	url, headers, expires, err := s.H.PresignPart(ctx, m.GetUploadId(), m.GetPartNumber(), m.GetTtl().AsDuration())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.PresignPartResponse{
		UploadUrl: presignedUrlProto(url, "PUT", headers, expires, "", nil),
	}), nil
}

func (s *MultipartServer) CompleteMultipartUpload(ctx context.Context, req *connect.Request[pb.CompleteMultipartUploadRequest]) (*connect.Response[pb.Object], error) {
	m := req.Msg
	parts := make([]multipart.PartETag, 0, len(m.GetParts()))
	for _, p := range m.GetParts() {
		parts = append(parts, multipart.PartETag{
			PartNumber: p.GetPartNumber(),
			ETag:       p.GetEtag(),
		})
	}
	if err := s.H.CompleteMultipartUpload(ctx, multipart.CompleteArgs{
		UploadID: m.GetUploadId(),
		Parts:    parts,
	}); err != nil {
		return nil, err
	}
	// Return a minimal Object — the caller fetches full state via GetObject.
	return connect.NewResponse(&pb.Object{Name: m.GetObjectName()}), nil
}

func (s *MultipartServer) AbortMultipartUpload(ctx context.Context, req *connect.Request[pb.AbortMultipartUploadRequest]) (*connect.Response[pb.AbortMultipartUploadResponse], error) {
	if err := s.H.AbortMultipartUpload(ctx, req.Msg.GetUploadId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.AbortMultipartUploadResponse{}), nil
}

func (s *MultipartServer) ListParts(ctx context.Context, req *connect.Request[pb.ListPartsRequest]) (*connect.Response[pb.ListPartsResponse], error) {
	m := req.Msg
	parts, next, err := s.H.ListParts(ctx, m.GetUploadId(), m.GetPage().GetPageSize(), m.GetPage().GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &pb.ListPartsResponse{Page: pageResponseProto(next)}
	for i := range parts {
		out.Parts = append(out.Parts, &pb.PartInfo{
			PartNumber: parts[i].PartNumber,
			SizeBytes:  parts[i].SizeBytes,
			Etag:       parts[i].ETag,
			UploadedAt: tsProto(parts[i].UploadedAt),
		})
	}
	return connect.NewResponse(out), nil
}

var _ paladindatav1connect.MultipartUploadServiceHandler = (*MultipartServer)(nil)

// silence unused
var _ = durationpb.New
var _ = commonpb.PageResponse{}
