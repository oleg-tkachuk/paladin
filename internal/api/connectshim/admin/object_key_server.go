package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

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
	tenantID, err := tenantUUIDFromParent(m.GetParent())
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
	_, name, err := objectKeyParts(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.GetObjectKey(ctx, name)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectKeyDomainToProto(out)), nil
}

func (s *ObjectKeyServer) UpdateObjectKey(ctx context.Context, req *connect.Request[pb.UpdateObjectKeyRequest]) (*connect.Response[pb.ObjectKey], error) {
	m := req.Msg
	tenantID, name, err := objectKeyParts(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	src := m.GetObjectKeyResource()
	args := objectkey.UpdateObjectKeyArgs{
		TenantID:        tenantID,
		ObjectKey:       name,
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
	_, name, err := objectKeyParts(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(req.Msg.GetResourceVersion())
	if err := s.H.DeleteObjectKey(ctx, name, rv); err != nil {
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
		if id, err := tenantUUIDFromParent(m.GetParent()); err == nil {
			args.TenantID = id
		}
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
	tenantID, name, err := objectKeyParts(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(req.Msg.GetResourceVersion())
	policy := req.Msg.GetCedarPolicy()
	out, err := s.H.UpdateObjectKey(ctx, objectkey.UpdateObjectKeyArgs{
		TenantID:        tenantID,
		ObjectKey:       name,
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
	_, name, err := objectKeyParts(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, _ := parseRV(m.GetResourceVersion())
	out, err := s.H.BindObjectKeyToBucket(ctx, name, m.GetBucket(), rv)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectKeyDomainToProto(out)), nil
}

var _ paladinadminv1connect.ObjectKeyServiceHandler = (*ObjectKeyServer)(nil)

// objectKeyParts decodes "tenants/{t}/objectKeys/{ok}".
func objectKeyParts(name string) (uuid.UUID, string, error) {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "tenants" || parts[2] != "objectKeys" {
		return uuid.Nil, "", fmt.Errorf("invalid object_key name %q", name)
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return uuid.Nil, "", err
	}
	return id, parts[3], nil
}

func tenantUUIDFromParent(parent string) (uuid.UUID, error) {
	if parent == "" {
		return uuid.Nil, nil
	}
	idStr, err := tenantIDFromName(parent)
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(idStr)
}

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
