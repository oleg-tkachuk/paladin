package connectshim

import (
	"context"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/object_key"
)

type ObjectKeyServer struct {
	paladinv1connect.UnimplementedObjectKeyServiceHandler
	H *objectkey.Handler
}

func NewObjectKeyServer(h *objectkey.Handler) *ObjectKeyServer { return &ObjectKeyServer{H: h} }

func objectKeyToProto(b *objectkey.ObjectKey) *pb.ObjectKey {
	if b == nil {
		return nil
	}
	return &pb.ObjectKey{
		Name:            fmt.Sprintf("object_keys/%s", b.ObjectKey),
		DisplayName:     b.DisplayName,
		ObjectKey:       b.ObjectKey,
		BackendId:       b.BackendID,
		BucketName:      b.BucketName,
		Policy:          &pb.ObjectKeyPolicy{CedarPolicy: b.CedarPolicy},
		ResourceVersion: resourceVersion(b.ResourceVersion),
		CreatedAt:       tsProto(b.CreatedAt),
		UpdatedAt:       tsProto(b.UpdatedAt),
	}
}

func (s *ObjectKeyServer) CreateObjectKey(ctx context.Context, req *connect.Request[pb.CreateObjectKeyRequest]) (*connect.Response[pb.ObjectKey], error) {
	m := req.Msg
	pol := m.GetPolicy()
	var rules []byte
	if pol != nil && len(pol.GetLifecycleRules()) > 0 {
		rules, _ = json.Marshal(pol.GetLifecycleRules())
	}
	b, err := s.H.CreateObjectKey(ctx, objectkey.CreateObjectKeyArgs{
		ObjectKey:      m.GetObjectKey(),
		DisplayName:    m.GetDisplayName(),
		BackendID:      m.GetBackendId(),
		BucketName:     m.GetBucketName(),
		CedarPolicy:    pol.GetCedarPolicy(),
		LifecycleRules: rules,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectKeyToProto(b)), nil
}

func (s *ObjectKeyServer) GetObjectKey(ctx context.Context, req *connect.Request[pb.GetObjectKeyRequest]) (*connect.Response[pb.ObjectKey], error) {
	bid, err := parseObjectKeyName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	b, err := s.H.GetObjectKey(ctx, bid)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectKeyToProto(b)), nil
}

func (s *ObjectKeyServer) UpdateObjectKey(ctx context.Context, req *connect.Request[pb.UpdateObjectKeyRequest]) (*connect.Response[pb.ObjectKey], error) {
	m := req.Msg
	bid, err := parseObjectKeyName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseResourceVersion(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	args := objectkey.UpdateObjectKeyArgs{ObjectKey: bid, ExpectedVersion: rv}
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
	b, err := s.H.UpdateObjectKey(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectKeyToProto(b)), nil
}

func (s *ObjectKeyServer) DeleteObjectKey(ctx context.Context, req *connect.Request[pb.DeleteObjectKeyRequest]) (*connect.Response[pb.DeleteObjectKeyResponse], error) {
	bid, err := parseObjectKeyName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseResourceVersion(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.H.DeleteObjectKey(ctx, bid, rv); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteObjectKeyResponse{}), nil
}

func (s *ObjectKeyServer) ListObjectKeys(ctx context.Context, req *connect.Request[pb.ListObjectKeysRequest]) (*connect.Response[pb.ListObjectKeysResponse], error) {
	bs, next, err := s.H.ListObjectKeys(ctx, objectkey.ListObjectKeysArgs{
		PageSize:  req.Msg.GetPageSize(),
		PageToken: req.Msg.GetPageToken(),
	})
	if err != nil {
		return nil, err
	}
	out := &pb.ListObjectKeysResponse{NextPageToken: next}
	for i := range bs {
		out.ObjectKeys = append(out.ObjectKeys, objectKeyToProto(&bs[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *ObjectKeyServer) GetObjectKeyStats(ctx context.Context, req *connect.Request[pb.GetObjectKeyStatsRequest]) (*connect.Response[pb.ObjectKeyStats], error) {
	bid, err := parseObjectKeyName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	st, err := s.H.GetObjectKeyStats(ctx, bid)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.ObjectKeyStats{
		ApproximateObjectCount: st.ObjectCountAvailable + st.ObjectCountPending + st.ObjectCountDeleted,
		ApproximateTotalBytes:  st.SizeBytesAvailable,
		ObjectCountByState: map[string]int64{
			"AVAILABLE": st.ObjectCountAvailable,
			"PENDING":   st.ObjectCountPending,
			"DELETED":   st.ObjectCountDeleted,
		},
	}), nil
}

var _ paladinv1connect.ObjectKeyServiceHandler = (*ObjectKeyServer)(nil)
