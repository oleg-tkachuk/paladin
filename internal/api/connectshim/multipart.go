package connectshim

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
)

type MultipartServer struct {
	paladinv1connect.UnimplementedMultipartUploadServiceHandler
	H *multipart.Handler
}

func NewMultipartServer(h *multipart.Handler) *MultipartServer { return &MultipartServer{H: h} }

func (s *MultipartServer) InitiateMultipartUpload(ctx context.Context, req *connect.Request[pb.InitiateMultipartUploadRequest]) (*connect.Response[pb.InitiateMultipartUploadResponse], error) {
	m := req.Msg
	totalParts, partSize := planParts(m.GetSizeBytes())
	session, err := s.H.InitiateMultipartUpload(ctx, multipart.InitiateArgs{
		ObjectKey:     m.GetObjectKey(),
		Key:           m.GetKey(),
		ContentType:   m.GetContentType(),
		SizeHint:      m.GetSizeBytes(),
		TotalParts:    totalParts,
		PartSizeBytes: partSize,
		ChecksumAlgo:  checksumAlgoStr(m.GetChecksumAlgorithm()),
		Metadata:      m.GetMetadata(),
		Tags:          m.GetTags(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.InitiateMultipartUploadResponse{
		Object: &pb.Object{
			Name:      fmt.Sprintf("object_keys/%s/objects/%s", session.ObjectKey, session.ObjectID),
			ObjectId:  session.ObjectID.String(),
			ObjectKey: session.ObjectKey,
			Key:       session.Key,
			State:     pb.ObjectState_OBJECT_STATE_PENDING,
		},
		UploadId:            session.UploadID,
		RecommendedPartSize: session.PartSizeBytes,
		TotalParts:          session.TotalParts,
	}), nil
}

func (s *MultipartServer) CompleteMultipartUpload(ctx context.Context, req *connect.Request[pb.CompleteMultipartUploadRequest]) (*connect.Response[pb.Object], error) {
	parts := make([]multipart.PartETag, 0, len(req.Msg.GetParts()))
	for _, p := range req.Msg.GetParts() {
		parts = append(parts, multipart.PartETag{PartNumber: p.GetPartNumber(), ETag: p.GetEtag()})
	}
	if err := s.H.CompleteMultipartUpload(ctx, multipart.CompleteArgs{
		UploadID: req.Msg.GetUploadId(),
		Parts:    parts,
	}); err != nil {
		return nil, err
	}
	// Handler doesn't return an Object; callers fetch via GetObject if needed.
	return connect.NewResponse(&pb.Object{Name: req.Msg.GetObjectName()}), nil
}

func (s *MultipartServer) AbortMultipartUpload(ctx context.Context, req *connect.Request[pb.AbortMultipartUploadRequest]) (*connect.Response[pb.AbortMultipartUploadResponse], error) {
	if err := s.H.AbortMultipartUpload(ctx, req.Msg.GetUploadId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.AbortMultipartUploadResponse{}), nil
}

// planParts picks a part size that keeps count <= 10_000 (S3 limit) while
// respecting the S3 minimum part size (5 MiB) for any part that isn't last.
func planParts(size int64) (totalParts int32, partSize int64) {
	const (
		minPart = int64(5 << 20) // 5 MiB
		defPart = int64(8 << 20) // 8 MiB
		maxN    = int32(10_000)
	)
	if size <= 0 {
		return 0, defPart
	}
	partSize = defPart
	if size/partSize > int64(maxN) {
		partSize = (size + int64(maxN) - 1) / int64(maxN)
		if partSize < minPart {
			partSize = minPart
		}
	}
	n := (size + partSize - 1) / partSize
	return int32(n), partSize
}

var _ paladinv1connect.MultipartUploadServiceHandler = (*MultipartServer)(nil)
