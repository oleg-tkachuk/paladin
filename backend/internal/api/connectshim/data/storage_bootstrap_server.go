package data

import (
	"context"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/storagebootstrap"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

// StorageBootstrapServer adapts the generated Connect handler onto the
// storagebootstrap.Handler business logic. Mounted on the DATA plane so an
// aud=data API token can reach it.
type StorageBootstrapServer struct {
	paladindatav1connect.UnimplementedStorageBootstrapServiceHandler
	H storageBootstrapHandler
}

func NewStorageBootstrapServer(h *storagebootstrap.Handler) *StorageBootstrapServer {
	return &StorageBootstrapServer{H: h}
}

func (s *StorageBootstrapServer) EnsureTenantStorage(ctx context.Context, req *connect.Request[pb.EnsureTenantStorageRequest]) (*connect.Response[pb.EnsureTenantStorageResponse], error) {
	m := req.Msg
	res, err := s.H.EnsureTenantStorage(ctx, m.GetBackendId(), m.GetBucket(), m.GetCollections())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.EnsureTenantStorageResponse{
		BucketCreated:       res.BucketCreated,
		CollectionsCreated:  res.CollectionsCreated,
		CollectionsExisting: res.CollectionsExisting,
	}), nil
}

var _ paladindatav1connect.StorageBootstrapServiceHandler = (*StorageBootstrapServer)(nil)
