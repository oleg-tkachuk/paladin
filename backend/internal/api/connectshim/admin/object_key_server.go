package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/connectshim/resolve"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/object_key"
)

type ObjectKeyServer struct {
	paladinadminv1connect.UnimplementedObjectKeyServiceHandler
	H *objectkey.Handler
}

func NewObjectKeyServer(h *objectkey.Handler) *ObjectKeyServer { return &ObjectKeyServer{H: h} }

func (s *ObjectKeyServer) CreateObjectKey(ctx context.Context, req *connect.Request[pb.CreateObjectKeyRequest]) (*connect.Response[pb.ObjectKey], error) {
	m := req.Msg
	tenantID, err := resolve.ResolveTenantParent(m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	src := m.GetObjectKeyResource()
	backend, bucket, _ := bucketRef(src.GetBucket())
	args := objectkey.CreateObjectKeyArgs{
		TenantID:    tenantID,
		ObjectKey:   m.GetObjectKey(),
		DisplayName: src.GetDisplayName(),
		BackendID:   backend,
		BucketName:  bucket,
		CedarPolicy: src.GetCedarPolicy(),
	}
	out, err := s.H.CreateObjectKey(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectKeyDomainToProto(out)), nil
}

func (s *ObjectKeyServer) GetObjectKey(ctx context.Context, req *connect.Request[pb.GetObjectKeyRequest]) (*connect.Response[pb.ObjectKey], error) {
	ref, err := resolve.ResolveObjectKeyName(ctx, req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.GetObjectKey(ctx, ref.TenantID, ref.ObjectKey)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectKeyDomainToProto(out)), nil
}

func (s *ObjectKeyServer) UpdateObjectKey(ctx context.Context, req *connect.Request[pb.UpdateObjectKeyRequest]) (*connect.Response[pb.ObjectKey], error) {
	m := req.Msg
	ref, err := resolve.ResolveObjectKeyName(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	src := m.GetObjectKeyResource()
	args := objectkey.UpdateObjectKeyArgs{
		TenantID:        ref.TenantID,
		ObjectKey:       ref.ObjectKey,
		ExpectedVersion: rv,
	}
	mask := m.GetUpdateMask().GetPaths()
	if slices.Contains(mask, "display_name") {
		v := src.GetDisplayName()
		args.DisplayName = &v
	}
	if slices.Contains(mask, "cedar_policy") {
		v := src.GetCedarPolicy()
		args.CedarPolicy = &v
	}
	out, err := s.H.UpdateObjectKey(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectKeyDomainToProto(out)), nil
}

func (s *ObjectKeyServer) DeleteObjectKey(ctx context.Context, req *connect.Request[pb.DeleteObjectKeyRequest]) (*connect.Response[pb.DeleteObjectKeyResponse], error) {
	ref, err := resolve.ResolveObjectKeyName(ctx, req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(req.Msg.GetResourceVersion())
	if err := s.H.DeleteObjectKey(ctx, ref.ObjectKey, rv); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteObjectKeyResponse{}), nil
}

func (s *ObjectKeyServer) ListObjectKeys(ctx context.Context, req *connect.Request[pb.ListObjectKeysRequest]) (*connect.Response[pb.ListObjectKeysResponse], error) {
	m := req.Msg
	args := objectkey.ListObjectKeysArgs{
		PageSize:  m.GetPage().GetPageSize(),
		PageToken: m.GetPage().GetPageToken(),
	}
	if m.GetParent() != "" {
		if id, err := resolve.ResolveTenantParent(m.GetParent()); err == nil {
			args.TenantID = id
		}
	}
	if b := m.GetBucket(); b != "" {
		backend, bucket, err := bucketRef(b)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("bucket: %w", err))
		}
		args.BackendID = backend
		args.BucketName = bucket
	}
	list, next, err := s.H.ListObjectKeys(ctx, args)
	if err != nil {
		return nil, err
	}
	out := &pb.ListObjectKeysResponse{Page: pageResponseProto(next)}
	for i := range list {
		out.ObjectKeys = append(out.ObjectKeys, objectKeyDomainToProto(&list[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *ObjectKeyServer) SetObjectKeyPolicy(ctx context.Context, req *connect.Request[pb.SetObjectKeyPolicyRequest]) (*connect.Response[pb.ObjectKey], error) {
	ref, err := resolve.ResolveObjectKeyName(ctx, req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(req.Msg.GetResourceVersion())
	policy := req.Msg.GetCedarPolicy()
	out, err := s.H.UpdateObjectKey(ctx, objectkey.UpdateObjectKeyArgs{
		TenantID:        ref.TenantID,
		ObjectKey:       ref.ObjectKey,
		ExpectedVersion: rv,
		CedarPolicy:     &policy,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectKeyDomainToProto(out)), nil
}

func (s *ObjectKeyServer) BindObjectKeyToBucket(ctx context.Context, req *connect.Request[pb.BindObjectKeyToBucketRequest]) (*connect.Response[pb.ObjectKey], error) {
	m := req.Msg
	ref, err := resolve.ResolveObjectKeyName(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(m.GetResourceVersion())
	out, err := s.H.BindObjectKeyToBucket(ctx, ref.ObjectKey, m.GetBucket(), rv)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectKeyDomainToProto(out)), nil
}

var _ paladinadminv1connect.ObjectKeyServiceHandler = (*ObjectKeyServer)(nil)

// bucketRef decodes "storageBackends/{backend}/buckets/{bucket}".
func bucketRef(name string) (backend, bucket string, err error) {
	if name == "" {
		return "", "", nil
	}
	return bucketNameParts(name)
}

func objectKeyDomainToProto(o *objectkey.ObjectKey) *pb.ObjectKey {
	if o == nil {
		return nil
	}
	return &pb.ObjectKey{
		Name:            fmt.Sprintf("tenants/%s/objectKeys/%s", o.TenantID, o.ObjectKey),
		TenantId:        o.TenantID.String(),
		ObjectKey:       o.ObjectKey,
		DisplayName:     o.DisplayName,
		Bucket:          fmt.Sprintf("storageBackends/%s/buckets/%s", o.BackendID, o.BucketName),
		CedarPolicy:     o.CedarPolicy,
		ResourceVersion: resourceVersion(o.ResourceVersion),
		CreatedAt:       tsProto(o.CreatedAt),
		UpdatedAt:       tsProto(o.UpdatedAt),
	}
}

// silence unused imports warning if needed
var _ = json.Marshal
