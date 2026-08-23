package data

import (
	"context"
	"fmt"

	"github.com/google/uuid"

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
	collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	sess, err := s.H.InitiateMultipartUpload(ctx, multipart.InitiateArgs{
		Collection:   collection,
		Key:          m.GetKey(),
		ContentType:  m.GetContentType(),
		SizeHint:     m.GetSizeBytes(),
		ChecksumAlgo: checksumAlgoStr(m.GetChecksumAlgorithm()),
		Metadata:     m.GetMetadata(),
		Tags:         m.GetTags(),
		ExternalRef:  m.GetExternalRef(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.InitiateMultipartUploadResponse{
		// Object's full state is fetched lazily via GetObject; we surface the
		// minimal envelope here — plus `name`, without which the caller
		// cannot continue: PresignPart and Complete both address the upload
		// by object_name, and there is nowhere else to get it.
		Object: &pb.Object{
			Name: fmt.Sprintf("tenants/%s/collections/%s/objects/%s",
				sess.TenantID, sess.Collection, sess.ObjectID),
			ObjectId:   sess.ObjectID.String(),
			TenantId:   sess.TenantID.String(),
			Collection: sess.Collection,
			Key:        sess.Key,
		},
		UploadId:            sess.UploadID,
		RecommendedPartSize: sess.PartSizeBytes,
		TotalParts:          sess.TotalParts,
	}), nil
}

func (s *MultipartServer) PresignPart(ctx context.Context, req *connect.Request[pb.PresignPartRequest]) (*connect.Response[pb.PresignPartResponse], error) {
	m := req.Msg
	want, err := sessionRef(ctx, m.GetObjectName())
	if err != nil {
		return nil, err
	}
	url, headers, expires, err := s.H.PresignPart(ctx, m.GetUploadId(), m.GetPartNumber(), m.GetTtl().AsDuration(), want)
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
	want, err := sessionRef(ctx, req.Msg.GetObjectName())
	if err != nil {
		return nil, err
	}
	if err := s.H.AbortMultipartUpload(ctx, req.Msg.GetUploadId(), want); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.AbortMultipartUploadResponse{}), nil
}

func (s *MultipartServer) ListParts(ctx context.Context, req *connect.Request[pb.ListPartsRequest]) (*connect.Response[pb.ListPartsResponse], error) {
	m := req.Msg
	want, err := sessionRef(ctx, m.GetObjectName())
	if err != nil {
		return nil, err
	}
	parts, next, err := s.H.ListParts(ctx, m.GetUploadId(), m.GetPage().GetPageSize(), m.GetPage().GetPageToken(), want)
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

// sessionRef parses the object_name every per-session multipart RPC carries.
// The name is the caller's statement of which object the upload belongs to;
// the handler checks it against the session. An unparseable name is rejected
// here rather than silently treated as "no claim".
func sessionRef(ctx context.Context, objectName string) (multipart.SessionRef, error) {
	collection, objectID, err := objectNameParts(ctx, objectName)
	if err != nil {
		return multipart.SessionRef{}, badName(err)
	}
	id, err := uuid.Parse(objectID)
	if err != nil {
		return multipart.SessionRef{}, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid object_id in object_name: %w", err))
	}
	return multipart.SessionRef{Collection: collection, ObjectID: id}, nil
}
