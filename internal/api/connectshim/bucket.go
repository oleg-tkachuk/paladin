package connectshim

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/bucket"
)

// BucketServer bridges generated Connect to bucket.Handler.
type BucketServer struct {
	paladinv1connect.UnimplementedBucketServiceHandler
	H *bucket.Handler
}

func NewBucketServer(h *bucket.Handler) *BucketServer { return &BucketServer{H: h} }

func (s *BucketServer) CreateBucket(ctx context.Context, req *connect.Request[pb.CreateBucketRequest]) (*connect.Response[pb.Bucket], error) {
	m := req.Msg
	labelBytes, _ := json.Marshal(m.GetLabels())
	b, err := s.H.CreateBucket(ctx, bucket.CreateArgs{
		BackendID:   m.GetBackendId(),
		BucketName:  m.GetBucketName(),
		DisplayName: m.GetDisplayName(),
		Region:      m.GetRegion(),
		Labels:      labelBytes,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(b)), nil
}

func (s *BucketServer) GetBucket(ctx context.Context, req *connect.Request[pb.GetBucketRequest]) (*connect.Response[pb.Bucket], error) {
	backendID, bucketName, err := parseBucketName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	b, err := s.H.GetBucket(ctx, backendID, bucketName)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(b)), nil
}

func (s *BucketServer) UpdateBucket(ctx context.Context, req *connect.Request[pb.UpdateBucketRequest]) (*connect.Response[pb.Bucket], error) {
	m := req.Msg
	backendID, bucketName, err := parseBucketName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseResourceVersion(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	args := bucket.UpdateArgs{BackendID: backendID, BucketName: bucketName, ExpectedVersion: rv}
	for _, path := range m.GetUpdateMask().GetPaths() {
		switch path {
		case "display_name":
			v := m.GetDisplayName()
			args.DisplayName = &v
		case "labels":
			lb, _ := json.Marshal(m.GetLabels())
			args.Labels = lb
		}
	}
	b, err := s.H.UpdateBucket(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(b)), nil
}

func (s *BucketServer) DeleteBucket(ctx context.Context, req *connect.Request[pb.DeleteBucketRequest]) (*connect.Response[pb.DeleteBucketResponse], error) {
	backendID, bucketName, err := parseBucketName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseResourceVersion(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.H.DeleteBucket(ctx, backendID, bucketName, rv, req.Msg.GetDeleteRemote()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteBucketResponse{}), nil
}

func (s *BucketServer) ListBuckets(ctx context.Context, req *connect.Request[pb.ListBucketsRequest]) (*connect.Response[pb.ListBucketsResponse], error) {
	m := req.Msg
	args := bucket.ListArgs{
		PageSize:  m.GetPageSize(),
		PageToken: m.GetPageToken(),
	}
	if m.GetBackendId() != "" {
		bid := m.GetBackendId()
		args.BackendID = &bid
	}
	bs, next, err := s.H.ListBuckets(ctx, args)
	if err != nil {
		return nil, err
	}
	out := &pb.ListBucketsResponse{NextPageToken: next}
	for i := range bs {
		out.Buckets = append(out.Buckets, bucketToProto(&bs[i]))
	}
	return connect.NewResponse(out), nil
}

var _ paladinv1connect.BucketServiceHandler = (*BucketServer)(nil)

// parseBucketName extracts (backendID, bucketName) from
// "backends/{backend_id}/buckets/{bucket_name}".
func parseBucketName(name string) (string, string, error) {
	const backendsPrefix = "backends/"
	const bucketsSep = "/buckets/"
	if !strings.HasPrefix(name, backendsPrefix) {
		return "", "", fmt.Errorf("invalid bucket name %q", name)
	}
	rest := name[len(backendsPrefix):]
	idx := strings.Index(rest, bucketsSep)
	if idx <= 0 {
		return "", "", fmt.Errorf("invalid bucket name %q", name)
	}
	backendID := rest[:idx]
	bucketName := rest[idx+len(bucketsSep):]
	if bucketName == "" {
		return "", "", fmt.Errorf("invalid bucket name %q", name)
	}
	return backendID, bucketName, nil
}

func bucketToProto(b *bucket.Bucket) *pb.Bucket {
	if b == nil {
		return nil
	}
	var labels map[string]string
	if len(b.Labels) > 0 {
		_ = json.Unmarshal(b.Labels, &labels)
	}
	return &pb.Bucket{
		Name:            fmt.Sprintf("backends/%s/buckets/%s", b.BackendID, b.BucketName),
		BackendId:       b.BackendID,
		BucketName:      b.BucketName,
		DisplayName:     b.DisplayName,
		Region:          b.Region,
		Labels:          labels,
		ResourceVersion: resourceVersion(b.ResourceVersion),
		CreatedAt:       tsProto(b.CreatedAt),
		UpdatedAt:       tsProto(b.UpdatedAt),
	}
}
