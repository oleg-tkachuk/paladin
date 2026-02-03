package httpapi

import (
	"net/http"

	"paladin/internal/generated/api"
	"paladin/internal/service"

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

func (s *OpenAPIAdapter) CreateObject(c *gin.Context) {
	var req api.CreateObjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	var labels map[string]string
	if req.Labels != nil {
		labels = *req.Labels
	}

	// svc.CreateSingle expects *string for ExternalRef which matches req.ExternalRef
	id, key, p, err := s.svc.CreateSingle(c.Request.Context(), tenantID(c), req.ContentType, req.SizeBytes, nil, labels, req.ExternalRef)
	if err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, api.CreateObjectResponse{
		ObjectId:  &id,
		ObjectKey: &key,
		UploadUrl: &p.URL,
		Method:    &p.Method,
		Headers:   &p.Headers,
		ExpiresAt: &p.ExpiresAt,
	})
}

func (s *OpenAPIAdapter) GetObject(c *gin.Context, id openapi_types.UUID) {
	rec, p, err := s.svc.Get(c.Request.Context(), tenantID(c), id)
	if err != nil {
		respondWithError(c, http.StatusNotFound, err)
		return
	}

	status := string(rec.Status)
	c.JSON(http.StatusOK, api.GetObjectResponse{
		ObjectId:    &id,
		ObjectKey:   &rec.ObjectKey,
		Bucket:      &rec.Bucket,
		ContentType: &rec.ContentType,
		SizeBytes:   &rec.SizeBytes,
		Status:      &status,
		Labels:      &rec.Labels,
		ExternalRef: rec.ExternalRef, // Already *string
		DownloadUrl: &p.URL,
		ExpiresAt:   &p.ExpiresAt,
	})
}

func (s *OpenAPIAdapter) DeleteObject(c *gin.Context, id openapi_types.UUID) {
	if err := s.svc.Delete(c.Request.Context(), tenantID(c), id); err != nil {
		respondWithError(c, http.StatusInternalServerError, err)
		return
	}

	status := "deleted"
	c.JSON(http.StatusOK, api.DeleteObjectResponse{Status: &status})
}

func (s *OpenAPIAdapter) CompleteObject(c *gin.Context, id openapi_types.UUID) {
	if err := s.svc.MarkComplete(c.Request.Context(), tenantID(c), id); err != nil {
		respondWithError(c, http.StatusInternalServerError, err)
		return
	}

	status := "active"
	c.JSON(http.StatusOK, api.CompleteObjectResponse{Status: &status})
}

func (s *OpenAPIAdapter) GetObjectMeta(c *gin.Context, id openapi_types.UUID) {
	rec, err := s.svc.GetMeta(c.Request.Context(), tenantID(c), id)
	if err != nil {
		respondWithError(c, http.StatusNotFound, err)
		return
	}

	status := string(rec.Status)
	c.JSON(http.StatusOK, api.GetObjectMetaResponse{
		ObjectId:    &id,
		ObjectKey:   &rec.ObjectKey,
		Bucket:      &rec.Bucket,
		ContentType: &rec.ContentType,
		SizeBytes:   &rec.SizeBytes,
		Status:      &status,
		Labels:      &rec.Labels,
		ExternalRef: rec.ExternalRef,
		ExpiresAt:   rec.ExpiresAt,
	})
}

func (s *OpenAPIAdapter) InitiateMultipart(c *gin.Context) {
	var req api.InitiateMultipartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	var labels map[string]string
	if req.Labels != nil {
		labels = *req.Labels
	}

	out, err := s.svc.InitiateMultipart(c.Request.Context(), tenantID(c), req.ContentType, req.SizeBytes, labels, req.ExternalRef)
	if err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, api.InitiateMultipartResponse{
		ObjectId:  &out.ObjectID,
		ObjectKey: &out.ObjectKey,
		UploadId:  &out.UploadID,
		PartSize:  &out.PartSize,
		ExpiresAt: &out.ExpiresAt,
	})
}

func (s *OpenAPIAdapter) SignPart(c *gin.Context, uploadId string, partNumber int) {
	p, err := s.svc.SignPart(c.Request.Context(), tenantID(c), uploadId, int32(partNumber))
	if err != nil {
		respondWithError(c, http.StatusNotFound, err)
		return
	}

	c.JSON(http.StatusOK, api.SignPartResponse{
		UploadUrl: &p.URL,
		Method:    &p.Method,
		ExpiresAt: &p.ExpiresAt,
	})
}

func (s *OpenAPIAdapter) CompleteMultipart(c *gin.Context, uploadId string) {
	var req api.CompleteMultipartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	parts := make([]service.CompletePart, 0, len(req.Parts))
	for _, p := range req.Parts {
		parts = append(parts, service.CompletePart{PartNumber: p.PartNumber, ETag: p.Etag})
	}

	objID, err := s.svc.CompleteMultipart(c.Request.Context(), tenantID(c), uploadId, parts)
	if err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	objIDStr := objID.String()
	status := "active"
	c.JSON(http.StatusOK, api.CompleteMultipartResponse{ObjectId: &objIDStr, Status: &status})
}

func (s *OpenAPIAdapter) AbortMultipart(c *gin.Context, uploadId string) {
	if err := s.svc.AbortMultipart(c.Request.Context(), tenantID(c), uploadId); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	status := "aborted"
	c.JSON(http.StatusOK, api.AbortMultipartResponse{Status: &status})
}
