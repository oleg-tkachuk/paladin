package httpapi

import (
	"net/http"

	"paladin/internal/generated/api"
	"paladin/internal/service"
	"paladin/internal/storage/s3"
	"paladin/internal/store/postgres"

	"github.com/gin-gonic/gin"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type OpenAPIAdapter struct {
	svc service.ObjectsService
}

// NewOpenAPIAdapter creates a new OpenAPIAdapter
func NewOpenAPIAdapter(svc service.ObjectsService) *OpenAPIAdapter {
	return &OpenAPIAdapter{svc: svc}
}

// Ensure OpenAPIAdapter implements api.ServerInterface
var _ api.ServerInterface = (*OpenAPIAdapter)(nil)

func (s *OpenAPIAdapter) Healthz(c *gin.Context) {
	c.Status(http.StatusOK)
}

func (s *OpenAPIAdapter) Readyz(c *gin.Context) {
	c.Status(http.StatusOK)
}

func (s *OpenAPIAdapter) Version(c *gin.Context) {
	c.JSON(http.StatusOK, api.VersionResponse{
		Service: "paladin",
		Version: "v1.1.0",
	})
}

func (s *OpenAPIAdapter) CreateObject(c *gin.Context, params api.CreateObjectParams) {
	var req api.CreateObjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	ttl := 0
	if req.UploadExpiresInSeconds != nil {
		ttl = *req.UploadExpiresInSeconds
	}

	out, err := s.svc.CreateSingle(c.Request.Context(), tenantID(c), req.ContentType, req.SizeBytes, mapLabels(req.Labels), req.ExternalRef, ttl, params.IdempotencyKey)
	if err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusCreated, api.CreateObjectResponse{
		ObjectId:  out.ID,
		ObjectKey: out.Key,
		Bucket:    out.Bucket,
		Status:    api.Pending,
		Upload:    mapSignedAction(out.Upload),
	})
}

func (s *OpenAPIAdapter) GetObject(c *gin.Context, id openapi_types.UUID) {
	rec, err := s.svc.Get(c.Request.Context(), tenantID(c), id)
	if err != nil {
		respondWithError(c, http.StatusNotFound, err)
		return
	}

	c.JSON(http.StatusOK, mapObjectCommon(rec))
}

func (s *OpenAPIAdapter) HeadObject(c *gin.Context, id openapi_types.UUID) {
	_, err := s.svc.GetMeta(c.Request.Context(), tenantID(c), id)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Status(http.StatusOK)
}

func (s *OpenAPIAdapter) DeleteObject(c *gin.Context, id openapi_types.UUID) {
	if err := s.svc.Delete(c.Request.Context(), tenantID(c), id); err != nil {
		respondWithError(c, http.StatusInternalServerError, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *OpenAPIAdapter) CompleteObject(c *gin.Context, id openapi_types.UUID) {
	var req api.CompleteObjectRequest
	_ = c.ShouldBindJSON(&req) // Optional body

	rec, err := s.svc.CompleteObject(c.Request.Context(), tenantID(c), id, req.Etag, req.SizeBytes)
	if err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, api.CompleteObjectResponse{
		Status:          mapStatus(rec.Status),
		StoredEtag:      rec.StoredETag,
		StoredSizeBytes: rec.StoredSizeBytes,
		CompletedAt:     rec.CompletedAt,
	})
}

func (s *OpenAPIAdapter) GetObjectMeta(c *gin.Context, id openapi_types.UUID) {
	rec, err := s.svc.GetMeta(c.Request.Context(), tenantID(c), id)
	if err != nil {
		respondWithError(c, http.StatusNotFound, err)
		return
	}

	c.JSON(http.StatusOK, mapObjectCommon(rec))
}

func (s *OpenAPIAdapter) PatchObjectMeta(c *gin.Context, id openapi_types.UUID) {
	var req api.PatchObjectMetaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	rec, err := s.svc.PatchMeta(c.Request.Context(), tenantID(c), id, mapLabels(req.Labels), req.ExternalRef)
	if err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, mapObjectCommon(rec))
}

func (s *OpenAPIAdapter) ListObjects(c *gin.Context, params api.ListObjectsParams) {
	limit := 100
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	filter := postgres.ListObjectsFilter{
		Status:      (*postgres.ObjectStatus)(params.Status),
		ExternalRef: params.ExternalRef,
	}

	items, next, err := s.svc.List(c.Request.Context(), tenantID(c), filter, limit, cursor)
	if err != nil {
		respondWithError(c, http.StatusInternalServerError, err)
		return
	}

	out := make([]api.ObjectCommon, len(items))
	for i, item := range items {
		out[i] = mapObjectCommon(&item)
	}

	c.JSON(http.StatusOK, api.ListObjectsResponse{
		Items:      out,
		NextCursor: &next,
	})
}

func (s *OpenAPIAdapter) SignObjectDownload(c *gin.Context, id openapi_types.UUID) {
	var req api.SignObjectDownloadRequest
	_ = c.ShouldBindJSON(&req)

	ttl := 0
	if req.DownloadExpiresInSeconds != nil {
		ttl = *req.DownloadExpiresInSeconds
	}

	p, err := s.svc.SignDownload(c.Request.Context(), tenantID(c), id, ttl)
	if err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, api.SignObjectDownloadResponse{
		ObjectId: id,
		Download: mapSignedAction(p),
	})
}

func (s *OpenAPIAdapter) SignObjectUpload(c *gin.Context, id openapi_types.UUID) {
	var req api.SignObjectUploadRequest
	_ = c.ShouldBindJSON(&req)

	ttl := 0
	if req.UploadExpiresInSeconds != nil {
		ttl = *req.UploadExpiresInSeconds
	}

	p, err := s.svc.SignUpload(c.Request.Context(), tenantID(c), id, ttl)
	if err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, api.SignObjectUploadResponse{
		ObjectId: id,
		Upload:   mapSignedAction(p),
	})
}

func (s *OpenAPIAdapter) InitiateMultipart(c *gin.Context, params api.InitiateMultipartParams) {
	var req api.InitiateMultipartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	ttl := 0
	if req.UploadExpiresInSeconds != nil {
		ttl = *req.UploadExpiresInSeconds
	}

	out, err := s.svc.InitiateMultipart(c.Request.Context(), tenantID(c), req.ContentType, req.SizeBytes, mapLabels(req.Labels), req.ExternalRef, ttl, params.IdempotencyKey)
	if err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, api.InitiateMultipartResponse{
		ObjectId:  out.ObjectID,
		ObjectKey: out.ObjectKey,
		UploadId:  out.UploadID,
		PartSize:  out.PartSize,
		ExpiresAt: out.ExpiresAt,
		Bucket:    out.Bucket, // Added Bucket to out struct previously? Let's check objects_service.go
		Status:    api.Uploading,
	})
}

func (s *OpenAPIAdapter) GetMultipart(c *gin.Context, uploadId string) {
	multi, err := s.svc.GetMultipart(c.Request.Context(), tenantID(c), uploadId)
	if err != nil {
		respondWithError(c, http.StatusNotFound, err)
		return
	}

	c.JSON(http.StatusOK, api.GetMultipartResponse{
		ObjectId:  multi.ObjectID,
		ObjectKey: multi.ObjectKey,
		UploadId:  multi.UploadID,
		PartSize:  multi.PartSize,
		Bucket:    multi.Bucket,
		Status:    mapStatus(postgres.ObjectStatus(multi.Status)), // Multi status is roughly same
		CreatedAt: multi.CreatedAt,
		UpdatedAt: multi.UpdatedAt,
	})
}

func (s *OpenAPIAdapter) AbortMultipart(c *gin.Context, uploadId string) {
	if err := s.svc.AbortMultipart(c.Request.Context(), tenantID(c), uploadId); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, api.AbortMultipartResponse{Status: api.Aborted})
}

func (s *OpenAPIAdapter) CompleteMultipart(c *gin.Context, uploadId string) {
	var req api.CompleteMultipartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	parts := make([]service.CompletePart, len(req.Parts))
	for i, p := range req.Parts {
		parts[i] = service.CompletePart{PartNumber: p.PartNumber, ETag: p.Etag}
	}

	rec, err := s.svc.CompleteMultipart(c.Request.Context(), tenantID(c), uploadId, parts)
	if err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, api.CompleteMultipartResponse{
		ObjectId:        rec.ID,
		Status:          mapStatus(rec.Status),
		StoredEtag:      rec.StoredETag,
		StoredSizeBytes: rec.StoredSizeBytes,
		CompletedAt:     rec.CompletedAt,
	})
}

func (s *OpenAPIAdapter) SignPartsBatch(c *gin.Context, uploadId string) {
	var req api.SignPartsBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	batch, err := s.svc.SignPartsBatch(c.Request.Context(), tenantID(c), uploadId, req.PartNumbers)
	if err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	out := make([]api.SignPartResponse, len(batch))
	for i, b := range batch {
		out[i] = api.SignPartResponse{
			PartNumber: b.PartNumber,
			Upload:     mapSignedAction(b.Upload),
		}
	}

	c.JSON(http.StatusOK, api.SignPartsBatchResponse{
		UploadId: uploadId,
		Parts:    out,
	})
}

func (s *OpenAPIAdapter) SignPart(c *gin.Context, uploadId string, partNumber int32) {
	p, err := s.svc.SignPart(c.Request.Context(), tenantID(c), uploadId, partNumber)
	if err != nil {
		respondWithError(c, http.StatusNotFound, err)
		return
	}

	c.JSON(http.StatusOK, api.SignPartResponse{
		PartNumber: partNumber,
		Upload:     mapSignedAction(p),
	})
}

// Helpers

func mapSignedAction(p s3.Presigned) api.SignedAction {
	return api.SignedAction{
		Url:       p.URL,
		Method:    api.SignedActionMethod(p.Method),
		Headers:   &p.Headers,
		ExpiresAt: p.ExpiresAt,
	}
}

func mapStatus(s postgres.ObjectStatus) api.ObjectStatus {
	return api.ObjectStatus(s)
}

func mapLabels(l *api.Labels) map[string]string {
	if l == nil {
		return nil
	}
	return (map[string]string)(*l)
}

func mapObjectCommon(rec *postgres.ObjectRecord) api.ObjectCommon {
	labels := api.Labels(rec.Labels)
	return api.ObjectCommon{
		ObjectId:        rec.ID,
		ObjectKey:       rec.ObjectKey,
		Bucket:          rec.Bucket,
		ContentType:     rec.ContentType,
		SizeBytes:       rec.SizeBytes,
		Status:          mapStatus(rec.Status),
		Labels:          &labels,
		ExternalRef:     rec.ExternalRef,
		CreatedAt:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
		CompletedAt:     rec.CompletedAt,
		DeletedAt:       rec.DeletedAt,
		StoredEtag:      rec.StoredETag,
		StoredSizeBytes: rec.StoredSizeBytes,
	}
}
