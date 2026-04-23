package connectshim

import (
	"context"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/bucket"
)

type BucketServer struct {
	paladinv1connect.UnimplementedBucketServiceHandler
	H *bucket.Handler
}

func NewBucketServer(h *bucket.Handler) *BucketServer { return &BucketServer{H: h} }

func bucketToProto(b *bucket.Bucket) *pb.Bucket {
	if b == nil {
		return nil
	}
	return &pb.Bucket{
		Name:            fmt.Sprintf("buckets/%s", b.BucketID),
		DisplayName:     b.DisplayName,
		BucketId:        b.BucketID,
		StorageBackend:  b.StorageBackend,
		Policy:          &pb.BucketPolicy{CedarPolicy: b.CedarPolicy},
		ResourceVersion: resourceVersion(b.ResourceVersion),
		CreatedAt:       tsProto(b.CreatedAt),
		UpdatedAt:       tsProto(b.UpdatedAt),
	}
}

func (s *BucketServer) CreateBucket(ctx context.Context, req *connect.Request[pb.CreateBucketRequest]) (*connect.Response[pb.Bucket], error) {
	m := req.Msg
	pol := m.GetPolicy()
	var rules []byte
	if pol != nil && len(pol.GetLifecycleRules()) > 0 {
		rules, _ = json.Marshal(pol.GetLifecycleRules())
	}
	b, err := s.H.CreateBucket(ctx, bucket.CreateBucketArgs{
		BucketID:       m.GetBucketId(),
		DisplayName:    m.GetDisplayName(),
		StorageBackend: m.GetStorageBackend(),
		CedarPolicy:    pol.GetCedarPolicy(),
		LifecycleRules: rules,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(b)), nil
}

func (s *BucketServer) GetBucket(ctx context.Context, req *connect.Request[pb.GetBucketRequest]) (*connect.Response[pb.Bucket], error) {
	bid, err := parseBucketName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	b, err := s.H.GetBucket(ctx, bid)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(b)), nil
}

func (s *BucketServer) UpdateBucket(ctx context.Context, req *connect.Request[pb.UpdateBucketRequest]) (*connect.Response[pb.Bucket], error) {
	m := req.Msg
	bid, err := parseBucketName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseResourceVersion(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	args := bucket.UpdateBucketArgs{BucketID: bid, ExpectedVersion: rv}
	for _, path := range m.GetUpdateMask().GetPaths() {
		switch path {
		case "display_name":
			v := m.GetDisplayName()
			args.DisplayName = &v
		case "policy.cedar_policy":
			v := m.GetPolicy().GetCedarPolicy()
			args.CedarPolicy = &v
		case "policy.lifecycle_rules":
			b, _ := json.Marshal(m.GetPolicy().GetLifecycleRules())
			args.LifecycleRules = b
		}
	}
	b, err := s.H.UpdateBucket(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(bucketToProto(b)), nil
}

func (s *BucketServer) DeleteBucket(ctx context.Context, req *connect.Request[pb.DeleteBucketRequest]) (*connect.Response[pb.DeleteBucketResponse], error) {
	bid, err := parseBucketName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseResourceVersion(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.H.DeleteBucket(ctx, bid, rv); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteBucketResponse{}), nil
}

func (s *BucketServer) ListBuckets(ctx context.Context, req *connect.Request[pb.ListBucketsRequest]) (*connect.Response[pb.ListBucketsResponse], error) {
	bs, next, err := s.H.ListBuckets(ctx, bucket.ListBucketsArgs{
		PageSize:  req.Msg.GetPageSize(),
		PageToken: req.Msg.GetPageToken(),
	})
	if err != nil {
		return nil, err
	}
	out := &pb.ListBucketsResponse{NextPageToken: next}
	for i := range bs {
		out.Buckets = append(out.Buckets, bucketToProto(&bs[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *BucketServer) GetBucketStats(ctx context.Context, req *connect.Request[pb.GetBucketStatsRequest]) (*connect.Response[pb.BucketStats], error) {
	bid, err := parseBucketName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	st, err := s.H.GetBucketStats(ctx, bid)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.BucketStats{
		ApproximateObjectCount: st.ObjectCountAvailable + st.ObjectCountPending + st.ObjectCountDeleted,
		ApproximateTotalBytes:  st.SizeBytesAvailable,
		ObjectCountByState: map[string]int64{
			"AVAILABLE": st.ObjectCountAvailable,
			"PENDING":   st.ObjectCountPending,
			"DELETED":   st.ObjectCountDeleted,
		},
	}), nil
}

var _ paladinv1connect.BucketServiceHandler = (*BucketServer)(nil)
