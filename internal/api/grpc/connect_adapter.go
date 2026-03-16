package grpcapi

import (
	"context"

	"connectrpc.com/connect"
)

// ConnectAdapter wraps the gRPC Server so it satisfies the generated
// PaladinServiceHandler interface (connect.Request / connect.Response).
type ConnectAdapter struct {
	srv *Server
}

// NewConnectAdapter returns a new ConnectAdapter backed by the given Server.
func NewConnectAdapter(srv *Server) *ConnectAdapter {
	return &ConnectAdapter{srv: srv}
}

// ─── Object RPCs ─────────────────────────────────────────────────────────────

func (a *ConnectAdapter) CreateObject(ctx context.Context, req *connect.Request[CreateObjectRequest]) (*connect.Response[CreateObjectResponse], error) {
	resp, err := a.srv.CreateObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) GetObject(ctx context.Context, req *connect.Request[GetObjectRequest]) (*connect.Response[GetObjectResponse], error) {
	resp, err := a.srv.GetObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) GetObjectMeta(ctx context.Context, req *connect.Request[GetObjectMetaRequest]) (*connect.Response[GetObjectMetaResponse], error) {
	resp, err := a.srv.GetObjectMeta(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) PatchObjectMeta(ctx context.Context, req *connect.Request[PatchObjectMetaRequest]) (*connect.Response[PatchObjectMetaResponse], error) {
	resp, err := a.srv.PatchObjectMeta(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) CompleteObject(ctx context.Context, req *connect.Request[CompleteObjectRequest]) (*connect.Response[CompleteObjectResponse], error) {
	resp, err := a.srv.CompleteObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) DeleteObject(ctx context.Context, req *connect.Request[DeleteObjectRequest]) (*connect.Response[DeleteObjectResponse], error) {
	resp, err := a.srv.DeleteObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) RestoreObject(ctx context.Context, req *connect.Request[RestoreObjectRequest]) (*connect.Response[RestoreObjectResponse], error) {
	resp, err := a.srv.RestoreObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) PurgeObject(ctx context.Context, req *connect.Request[PurgeObjectRequest]) (*connect.Response[PurgeObjectResponse], error) {
	resp, err := a.srv.PurgeObject(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// ─── Multipart RPCs ──────────────────────────────────────────────────────────

func (a *ConnectAdapter) InitiateMultipart(ctx context.Context, req *connect.Request[InitiateMultipartRequest]) (*connect.Response[InitiateMultipartResponse], error) {
	resp, err := a.srv.InitiateMultipart(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) SignPart(ctx context.Context, req *connect.Request[SignPartRequest]) (*connect.Response[SignPartResponse], error) {
	resp, err := a.srv.SignPart(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) CompleteMultipart(ctx context.Context, req *connect.Request[CompleteMultipartRequest]) (*connect.Response[CompleteMultipartResponse], error) {
	resp, err := a.srv.CompleteMultipart(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) AbortMultipart(ctx context.Context, req *connect.Request[AbortMultipartRequest]) (*connect.Response[AbortMultipartResponse], error) {
	resp, err := a.srv.AbortMultipart(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// ─── Category RPCs ───────────────────────────────────────────────────────────

func (a *ConnectAdapter) ListCategories(ctx context.Context, req *connect.Request[ListCategoriesRequest]) (*connect.Response[ListCategoriesResponse], error) {
	resp, err := a.srv.ListCategories(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) GetCategory(ctx context.Context, req *connect.Request[GetCategoryRequest]) (*connect.Response[GetCategoryResponse], error) {
	resp, err := a.srv.GetCategory(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) CreateCategory(ctx context.Context, req *connect.Request[CreateCategoryRequest]) (*connect.Response[CreateCategoryResponse], error) {
	resp, err := a.srv.CreateCategory(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) UpdateCategory(ctx context.Context, req *connect.Request[UpdateCategoryRequest]) (*connect.Response[UpdateCategoryResponse], error) {
	resp, err := a.srv.UpdateCategory(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) DeleteCategory(ctx context.Context, req *connect.Request[DeleteCategoryRequest]) (*connect.Response[DeleteCategoryResponse], error) {
	resp, err := a.srv.DeleteCategory(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) GetCategoryStats(ctx context.Context, req *connect.Request[GetCategoryStatsRequest]) (*connect.Response[GetCategoryStatsResponse], error) {
	resp, err := a.srv.GetCategoryStats(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// ─── Stats & List RPCs ───────────────────────────────────────────────────────

func (a *ConnectAdapter) GetObjectStats(ctx context.Context, req *connect.Request[GetObjectStatsRequest]) (*connect.Response[GetObjectStatsResponse], error) {
	resp, err := a.srv.GetObjectStats(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) ListObjects(ctx context.Context, req *connect.Request[ListObjectsRequest]) (*connect.Response[ListObjectsResponse], error) {
	resp, err := a.srv.ListObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// ─── Tenant RPCs ─────────────────────────────────────────────────────────────

func (a *ConnectAdapter) CreateTenant(ctx context.Context, req *connect.Request[CreateTenantRequest]) (*connect.Response[CreateTenantResponse], error) {
	resp, err := a.srv.CreateTenant(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) GetTenant(ctx context.Context, req *connect.Request[GetTenantRequest]) (*connect.Response[GetTenantResponse], error) {
	resp, err := a.srv.GetTenant(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) DeleteTenant(ctx context.Context, req *connect.Request[DeleteTenantRequest]) (*connect.Response[DeleteTenantResponse], error) {
	resp, err := a.srv.DeleteTenant(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) ListTenants(ctx context.Context, req *connect.Request[ListTenantsRequest]) (*connect.Response[ListTenantsResponse], error) {
	resp, err := a.srv.ListTenants(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) PatchTenantMetadata(ctx context.Context, req *connect.Request[PatchTenantMetadataRequest]) (*connect.Response[PatchTenantMetadataResponse], error) {
	resp, err := a.srv.PatchTenantMetadata(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

// ─── Bulk RPCs ───────────────────────────────────────────────────────────────

func (a *ConnectAdapter) BulkCreateObjects(ctx context.Context, req *connect.Request[BulkCreateObjectsRequest]) (*connect.Response[BulkCreateObjectsResponse], error) {
	resp, err := a.srv.BulkCreateObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) BulkDeleteObjects(ctx context.Context, req *connect.Request[BulkDeleteObjectsRequest]) (*connect.Response[BulkDeleteObjectsResponse], error) {
	resp, err := a.srv.BulkDeleteObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) BulkRestoreObjects(ctx context.Context, req *connect.Request[BulkRestoreObjectsRequest]) (*connect.Response[BulkRestoreObjectsResponse], error) {
	resp, err := a.srv.BulkRestoreObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) BulkPurgeObjects(ctx context.Context, req *connect.Request[BulkPurgeObjectsRequest]) (*connect.Response[BulkPurgeObjectsResponse], error) {
	resp, err := a.srv.BulkPurgeObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) BulkSignUploads(ctx context.Context, req *connect.Request[BulkSignUploadsRequest]) (*connect.Response[BulkSignUploadsResponse], error) {
	resp, err := a.srv.BulkSignUploads(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) BulkCompleteObjects(ctx context.Context, req *connect.Request[BulkCompleteObjectsRequest]) (*connect.Response[BulkCompleteObjectsResponse], error) {
	resp, err := a.srv.BulkCompleteObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (a *ConnectAdapter) BulkPatchObjects(ctx context.Context, req *connect.Request[BulkPatchObjectsRequest]) (*connect.Response[BulkPatchObjectsResponse], error) {
	resp, err := a.srv.BulkPatchObjects(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}
